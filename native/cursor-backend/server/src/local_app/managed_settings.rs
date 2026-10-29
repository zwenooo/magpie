//! Restore only the settings still owned by this backend, preserving later user edits.
use crate::{Error, Result};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{collections::BTreeMap, fs, path::Path};

#[derive(Serialize, Deserialize)]
struct Journal {
    before: BTreeMap<String, Option<Value>>,
    written: BTreeMap<String, Option<Value>>,
    // Values earlier runs wrote. This journal is saved before settings.json,
    // so a failed settings write leaves the file holding one of them.
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    earlier: BTreeMap<String, Vec<Option<Value>>>,
}

impl Journal {
    fn owned(self) -> BTreeMap<String, Vec<Option<Value>>> {
        let mut owned = self.earlier;
        for (key, value) in self.written {
            let values = owned.entry(key).or_default();
            if !values.contains(&value) {
                values.push(value);
            }
        }
        owned
    }
}

pub(super) fn apply(
    path: &Path,
    settings: &mut BTreeMap<String, Value>,
    proxy: &str,
) -> Result<()> {
    // Resume a crashed session without forgetting the user's original values.
    let mut earlier = BTreeMap::new();
    if let Some(old) = load(path)? {
        restore_values(settings, &old);
        earlier = old.owned();
    }
    let written = BTreeMap::from([
        (
            super::settings::KEYS[0].into(),
            Some(Value::String(proxy.into())),
        ),
        (
            super::settings::KEYS[1].into(),
            Some(Value::String(proxy.into())),
        ),
        (
            super::settings::KEYS[2].into(),
            Some(Value::String("on".into())),
        ),
        (super::settings::KEYS[3].into(), Some(Value::Bool(true))),
        (super::settings::KEYS[4].into(), Some(Value::Bool(true))),
        (super::settings::NO_PROXY_KEY.into(), None),
    ]);
    let before = written
        .keys()
        .map(|key: &String| (key.clone(), settings.get(key).cloned()))
        .collect();
    let journal = Journal {
        before,
        written,
        earlier,
    };
    let temp = path.with_extension("tmp");
    fs::write(&temp, serde_json::to_vec_pretty(&journal)?)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&temp, fs::Permissions::from_mode(0o600))?;
    }
    fs::rename(&temp, path)?;
    for (key, value) in journal.written {
        set(settings, key, value);
    }
    Ok(())
}

pub(super) fn restore(path: &Path, settings: &mut BTreeMap<String, Value>) -> Result<bool> {
    let Some(journal) = load(path)? else {
        return Ok(false);
    };
    restore_values(settings, &journal);
    Ok(true)
}

fn load(path: &Path) -> Result<Option<Journal>> {
    match fs::read(path) {
        Ok(data) => serde_json::from_slice(&data).map(Some).map_err(|error| {
            Error::Config(format!(
                "invalid Cursor settings ownership journal: {error}"
            ))
        }),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error.into()),
    }
}

fn restore_values(settings: &mut BTreeMap<String, Value>, journal: &Journal) {
    for (key, written) in &journal.written {
        let current = settings.get(key).cloned();
        let owned = current == *written
            || journal
                .earlier
                .get(key)
                .is_some_and(|values| values.contains(&current));
        if owned {
            if let Some(before) = journal.before.get(key) {
                set(settings, key.clone(), before.clone());
            }
        }
    }
}

fn set(settings: &mut BTreeMap<String, Value>, key: String, value: Option<Value>) {
    match value {
        Some(value) => {
            settings.insert(key, value);
        }
        None => {
            settings.remove(&key);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn restores_previous_proxy_and_preserves_user_edits() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("settings.json");
        let original = BTreeMap::from([
            (
                "http.proxy".into(),
                Value::String("http://company-proxy:80".into()),
            ),
            (
                "http.noProxy".into(),
                serde_json::json!(["internal.example"]),
            ),
            ("editor.fontSize".into(), serde_json::json!(16)),
        ]);
        let mut settings = original.clone();
        apply(&path, &mut settings, "http://127.0.0.1:12345").unwrap();
        // A restart changes ports, but still restores the original proxy.
        apply(&path, &mut settings, "http://127.0.0.1:54321").unwrap();
        settings.insert("http.proxySupport".into(), Value::String("off".into()));
        restore(&path, &mut settings).unwrap();
        let mut expected = original;
        expected.insert("http.proxySupport".into(), Value::String("off".into()));
        assert_eq!(settings, expected);
    }

    #[test]
    fn restores_originals_after_a_failed_settings_write() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("settings.json");
        let original = BTreeMap::from([
            (
                "http.proxy".into(),
                Value::String("http://company-proxy:80".into()),
            ),
            ("editor.fontSize".into(), serde_json::json!(16)),
        ]);
        let mut on_disk = original.clone();
        apply(&path, &mut on_disk, "http://127.0.0.1:12345").unwrap();
        // The next run saves its journal for a new port, then fails to
        // replace settings.json, which keeps the first run's proxy.
        apply(&path, &mut on_disk.clone(), "http://127.0.0.1:54321").unwrap();

        let mut disconnected = on_disk.clone();
        restore(&path, &mut disconnected).unwrap();
        assert_eq!(disconnected, original);

        let mut reconnected = on_disk;
        apply(&path, &mut reconnected, "http://127.0.0.1:23456").unwrap();
        restore(&path, &mut reconnected).unwrap();
        assert_eq!(reconnected, original);
    }

    #[test]
    fn reads_a_journal_saved_before_earlier_values() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("settings.json");
        fs::write(
            &path,
            br#"{"before":{"http.proxy":"http://company-proxy:80"},"written":{"http.proxy":"http://127.0.0.1:12345"}}"#,
        )
        .unwrap();
        let mut settings = BTreeMap::from([(
            "http.proxy".into(),
            Value::String("http://127.0.0.1:12345".into()),
        )]);
        restore(&path, &mut settings).unwrap();
        assert_eq!(
            settings.get("http.proxy"),
            Some(&Value::String("http://company-proxy:80".into()))
        );
    }

    #[test]
    fn corrupt_journal_never_overwrites_or_clears_settings() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("settings.json");
        fs::write(&path, b"broken").unwrap();
        let mut settings = BTreeMap::from([("http.proxy".into(), Value::String("mine".into()))]);
        let original = settings.clone();
        assert!(apply(&path, &mut settings, "http://127.0.0.1:1234").is_err());
        assert!(restore(&path, &mut settings).is_err());
        assert_eq!(settings, original);
    }
}
