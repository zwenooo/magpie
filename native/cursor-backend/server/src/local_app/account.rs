//! Integrates local Cursor account state.
use std::path::{Path, PathBuf};

use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use serde_json::json;
use sqlx::{Connection, SqliteConnection};

use crate::{Error, Result};

pub(crate) const EMAIL: &str = "cursor@magpie.local";
const SIGN_UP_TYPE: &str = "Google";
pub(crate) const SUBJECT: &str = "magpie-cursor-local-user";
const MEMBERSHIP_TYPE: &str = "ultra";
const SUBSCRIPTION_STATUS: &str = "active";

pub async fn inject_if_missing() -> Result<()> {
    let journal = crate::config::is_magpie_managed()
        .then(journal_path)
        .transpose()?;
    inject_if_missing_at(&state_db_path()?, journal.as_deref()).await
}

fn state_db_path() -> Result<PathBuf> {
    let home = dirs::home_dir()
        .ok_or_else(|| Error::Config("cannot resolve user home directory".into()))?;
    match std::env::consts::OS {
        "macos" => {
            Ok(home.join("Library/Application Support/Cursor/User/globalStorage/state.vscdb"))
        }
        "windows" => Ok(std::env::var_os("APPDATA")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join("AppData/Roaming"))
            .join("Cursor/User/globalStorage/state.vscdb")),
        "linux" => Ok(std::env::var_os("XDG_CONFIG_HOME")
            .map(PathBuf::from)
            .unwrap_or_else(|| home.join(".config"))
            .join("Cursor/User/globalStorage/state.vscdb")),
        platform => Err(Error::Config(format!(
            "Cursor account injection is unsupported on {platform}"
        ))),
    }
}

async fn inject_if_missing_at(path: &Path, journal: Option<&Path>) -> Result<()> {
    if let Some(parent) = path.parent() {
        tokio::fs::create_dir_all(parent).await?;
    }
    let options = sqlx::sqlite::SqliteConnectOptions::new()
        .filename(path)
        .create_if_missing(true);
    let mut connection = SqliteConnection::connect_with(&options).await?;
    sqlx::query(
        "CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)",
    )
    .execute(&mut connection)
    .await?;

    let token = local_token()?;
    // Cursor considers a session usable only with both tokens. Keep complete
    // user sessions (including opaque safeStorage values) without decrypting.
    // An incomplete pair must not prevent local setup from finishing.
    let complete_foreign_session: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM ItemTable WHERE key IN \
         ('cursorAuth/accessToken', 'cursorAuth/refreshToken') \
         AND length(trim(CAST(value AS TEXT))) > 0 AND CAST(value AS TEXT) != ?",
    )
    .bind(&token)
    .fetch_one(&mut connection)
    .await?;
    if complete_foreign_session == 2 {
        connection.close().await?;
        return Ok(());
    }

    let values = [
        ("cursorAuth/accessToken", token.as_str()),
        ("cursorAuth/refreshToken", token.as_str()),
        ("cursorAuth/cachedEmail", EMAIL),
        ("cursorAuth/cachedSignUpType", SIGN_UP_TYPE),
        ("cursorAuth/stripeMembershipAuthId", SUBJECT),
        ("cursorAuth/stripeMembershipType", MEMBERSHIP_TYPE),
        ("cursorAuth/stripeSubscriptionStatus", SUBSCRIPTION_STATUS),
    ];
    if let Some(journal) = journal {
        save_managed_journal(journal, &mut connection, &values).await?;
    }
    let mut transaction = connection.begin().await?;
    for (key, value) in values {
        sqlx::query("INSERT OR REPLACE INTO ItemTable(key, value) VALUES(?, ?)")
            .bind(key)
            .bind(value)
            .execute(&mut *transaction)
            .await?;
    }
    transaction.commit().await?;
    connection.close().await?;
    tracing::info!(
        email = EMAIL,
        subject = SUBJECT,
        "injected local Cursor account"
    );
    Ok(())
}

#[derive(serde::Serialize, serde::Deserialize)]
struct AccountJournal {
    before: std::collections::BTreeMap<String, Option<String>>,
    written: std::collections::BTreeMap<String, String>,
}

fn journal_path() -> Result<PathBuf> {
    Ok(crate::config::managed_data_dir()?.join("cursor-account-owned.json"))
}

async fn save_managed_journal(
    path: &Path,
    connection: &mut SqliteConnection,
    values: &[(&str, &str)],
) -> Result<()> {
    // A crash/reconnect retains the first snapshot. Parse failures never erase it.
    match std::fs::read(&path) {
        Ok(data) => {
            let _: AccountJournal = serde_json::from_slice(&data)?;
            return Ok(());
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }
    let mut journal = AccountJournal {
        before: Default::default(),
        written: Default::default(),
    };
    for (key, value) in values {
        let before = sqlx::query_scalar::<_, String>(
            "SELECT CAST(value AS TEXT) FROM ItemTable WHERE key = ?",
        )
        .bind(key)
        .fetch_optional(&mut *connection)
        .await?;
        journal.before.insert((*key).into(), before);
        journal.written.insert((*key).into(), (*value).into());
    }
    let temp = path.with_extension("tmp");
    std::fs::write(&temp, serde_json::to_vec(&journal)?)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&temp, std::fs::Permissions::from_mode(0o600))?;
    }
    std::fs::rename(temp, path)?;
    Ok(())
}

pub(super) async fn restore_managed() -> Result<()> {
    let path = journal_path()?;
    let journal: AccountJournal = match std::fs::read(&path) {
        Ok(data) => serde_json::from_slice(&data)?,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error.into()),
    };
    let options = sqlx::sqlite::SqliteConnectOptions::new()
        .filename(state_db_path()?)
        .create_if_missing(false);
    let mut connection = SqliteConnection::connect_with(&options).await?;
    restore_journal(&mut connection, &journal).await?;
    std::fs::remove_file(path)?;
    Ok(())
}

async fn restore_journal(
    connection: &mut SqliteConnection,
    journal: &AccountJournal,
) -> Result<()> {
    let mut transaction = connection.begin().await?;
    for (key, value) in &journal.written {
        match journal.before.get(key) {
            Some(Some(before)) => {
                sqlx::query(
                    "UPDATE ItemTable SET value = ? WHERE key = ? AND CAST(value AS TEXT) = ?",
                )
                .bind(before)
                .bind(key)
                .bind(value)
                .execute(&mut *transaction)
                .await?;
            }
            Some(None) => {
                sqlx::query("DELETE FROM ItemTable WHERE key = ? AND CAST(value AS TEXT) = ?")
                    .bind(key)
                    .bind(value)
                    .execute(&mut *transaction)
                    .await?;
            }
            None => {
                return Err(Error::Config(
                    "incomplete Cursor account ownership journal".into(),
                ))
            }
        }
    }
    transaction.commit().await?;
    Ok(())
}

pub(crate) fn is_local_cursor_authorization(authorization: &str) -> bool {
    authorization
        .strip_prefix("Bearer ")
        .is_some_and(is_local_cursor_token)
}

fn is_local_cursor_token(token: &str) -> bool {
    local_token().is_ok_and(|local| local == token)
}

pub(crate) fn local_token() -> Result<String> {
    let header = URL_SAFE_NO_PAD.encode(br#"{"alg":"HS256","typ":"JWT"}"#);
    let payload = URL_SAFE_NO_PAD.encode(serde_json::to_vec(&json!({
        "sub": SUBJECT,
        "email": EMAIL,
        "type": "session",
        "iss": "cursor-client",
        "scope": "openid profile email",
        "exp": 4070908800_u64
    }))?);
    Ok(format!("{header}.{payload}.{SUBJECT}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    async fn write_fixture(path: &Path, values: &[(&str, &str)]) {
        let options = sqlx::sqlite::SqliteConnectOptions::new()
            .filename(path)
            .create_if_missing(true);
        let mut connection = SqliteConnection::connect_with(&options).await.unwrap();
        sqlx::query("CREATE TABLE ItemTable (key TEXT UNIQUE, value BLOB)")
            .execute(&mut connection)
            .await
            .unwrap();
        for (key, value) in values {
            sqlx::query("INSERT INTO ItemTable VALUES (?, ?)")
                .bind(key)
                .bind(value)
                .execute(&mut connection)
                .await
                .unwrap();
        }
        connection.close().await.unwrap();
    }

    async fn fixture_rows(path: &Path) -> std::collections::BTreeMap<String, String> {
        let options = sqlx::sqlite::SqliteConnectOptions::new().filename(path);
        let mut connection = SqliteConnection::connect_with(&options).await.unwrap();
        let rows: Vec<(String, String)> =
            sqlx::query_as("SELECT key, CAST(value AS TEXT) FROM ItemTable ORDER BY key")
                .fetch_all(&mut connection)
                .await
                .unwrap();
        connection.close().await.unwrap();
        rows.into_iter().collect()
    }

    #[tokio::test]
    async fn incomplete_sessions_are_journaled_before_local_setup_and_restore_exactly() {
        let directory = tempfile::tempdir().unwrap();
        for (index, initial) in [
            vec![("cursorAuth/accessToken", "previous-access")],
            vec![("cursorAuth/refreshToken", "previous-refresh")],
            vec![
                ("cursorAuth/accessToken", "previous-access"),
                ("cursorAuth/refreshToken", " "),
            ],
            vec![
                ("cursorAuth/accessToken", ""),
                ("cursorAuth/refreshToken", "previous-refresh"),
            ],
        ]
        .into_iter()
        .enumerate()
        {
            let path = directory.path().join(format!("state-{index}.vscdb"));
            let journal_path = directory.path().join(format!("owned-{index}.json"));
            write_fixture(&path, &initial).await;
            let before = fixture_rows(&path).await;
            inject_if_missing_at(&path, Some(&journal_path))
                .await
                .unwrap();
            let written = fixture_rows(&path).await;
            let token = local_token().unwrap();
            assert_eq!(written["cursorAuth/accessToken"], token);
            assert_eq!(written["cursorAuth/refreshToken"], token);
            assert_eq!(written["cursorAuth/cachedEmail"], EMAIL);
            assert_eq!(written["cursorAuth/stripeMembershipAuthId"], SUBJECT);
            let journal_bytes = std::fs::read(&journal_path).unwrap();
            let journal: AccountJournal = serde_json::from_slice(&journal_bytes).unwrap();
            for (key, value) in &before {
                assert_eq!(journal.before.get(key), Some(&Some(value.clone())));
            }
            inject_if_missing_at(&path, Some(&journal_path))
                .await
                .unwrap();
            assert_eq!(std::fs::read(&journal_path).unwrap(), journal_bytes);
            let options = sqlx::sqlite::SqliteConnectOptions::new().filename(&path);
            let mut connection = SqliteConnection::connect_with(&options).await.unwrap();
            restore_journal(&mut connection, &journal).await.unwrap();
            connection.close().await.unwrap();
            assert_eq!(fixture_rows(&path).await, before);
        }
    }

    #[tokio::test]
    async fn complete_user_sessions_remain_opaque_and_unchanged() {
        let directory = tempfile::tempdir().unwrap();
        for (index, (access, refresh)) in [
            ("real-access", "real-refresh"),
            (
                "cursor-safe-storage:v1:opaque-access",
                "cursor-safe-storage:v1:opaque-refresh",
            ),
        ]
        .into_iter()
        .enumerate()
        {
            let path = directory.path().join(format!("state-{index}.vscdb"));
            let journal = directory.path().join(format!("owned-{index}.json"));
            write_fixture(
                &path,
                &[
                    ("cursorAuth/accessToken", access),
                    ("cursorAuth/refreshToken", refresh),
                    ("cursorAuth/cachedEmail", "existing@example.test"),
                ],
            )
            .await;
            let before = std::fs::read(&path).unwrap();
            inject_if_missing_at(&path, Some(&journal)).await.unwrap();
            assert_eq!(std::fs::read(&path).unwrap(), before);
            assert!(!journal.exists());
        }
    }

    #[tokio::test]
    async fn broken_account_journal_prevents_partial_session_changes() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("state.vscdb");
        let journal = directory.path().join("owned.json");
        write_fixture(&path, &[("cursorAuth/accessToken", "previous-access")]).await;
        std::fs::write(&journal, b"broken").unwrap();
        let before = fixture_rows(&path).await;
        assert!(inject_if_missing_at(&path, Some(&journal)).await.is_err());
        assert_eq!(fixture_rows(&path).await, before);
        assert_eq!(std::fs::read(&journal).unwrap(), b"broken");
    }

    #[tokio::test]
    async fn managed_restore_preserves_new_signin_and_restores_owned_fields() {
        let mut connection = SqliteConnection::connect("sqlite::memory:").await.unwrap();
        sqlx::query("CREATE TABLE ItemTable (key TEXT UNIQUE, value BLOB)")
            .execute(&mut connection)
            .await
            .unwrap();
        sqlx::query("INSERT INTO ItemTable VALUES ('token', 'user-signed-in'), ('email', 'ours'), ('membership', 'ours')")
            .execute(&mut connection).await.unwrap();
        let journal = AccountJournal {
            before: std::collections::BTreeMap::from([
                ("token".into(), None),
                ("email".into(), Some("original".into())),
                ("membership".into(), None),
            ]),
            written: std::collections::BTreeMap::from([
                ("token".into(), "ours".into()),
                ("email".into(), "ours".into()),
                ("membership".into(), "ours".into()),
            ]),
        };
        restore_journal(&mut connection, &journal).await.unwrap();
        let rows: Vec<(String, String)> =
            sqlx::query_as("SELECT key, CAST(value AS TEXT) FROM ItemTable ORDER BY key")
                .fetch_all(&mut connection)
                .await
                .unwrap();
        assert_eq!(
            rows,
            vec![
                ("email".into(), "original".into()),
                ("token".into(), "user-signed-in".into())
            ]
        );
    }

    #[tokio::test]
    async fn reinjection_repairs_local_membership_cache() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("state.vscdb");
        inject_if_missing_at(&path, None).await.unwrap();

        let mut connection = SqliteConnection::connect(&format!("sqlite:{}", path.display()))
            .await
            .unwrap();
        sqlx::query("UPDATE ItemTable SET value = 'free' WHERE key = ?")
            .bind("cursorAuth/stripeMembershipType")
            .execute(&mut connection)
            .await
            .unwrap();
        sqlx::query("DELETE FROM ItemTable WHERE key = ?")
            .bind("cursorAuth/stripeMembershipAuthId")
            .execute(&mut connection)
            .await
            .unwrap();
        drop(connection);

        inject_if_missing_at(&path, None).await.unwrap();

        let mut connection = SqliteConnection::connect(&format!("sqlite:{}", path.display()))
            .await
            .unwrap();
        let membership_type: String =
            sqlx::query_scalar("SELECT CAST(value AS TEXT) FROM ItemTable WHERE key = ?")
                .bind("cursorAuth/stripeMembershipType")
                .fetch_one(&mut connection)
                .await
                .unwrap();
        let membership_auth_id: String =
            sqlx::query_scalar("SELECT CAST(value AS TEXT) FROM ItemTable WHERE key = ?")
                .bind("cursorAuth/stripeMembershipAuthId")
                .fetch_one(&mut connection)
                .await
                .unwrap();

        assert_eq!(membership_type, MEMBERSHIP_TYPE);
        assert_eq!(membership_auth_id, SUBJECT);
    }

    #[test]
    fn recognizes_only_the_injected_cursor_token() {
        let token = local_token().unwrap();
        assert!(is_local_cursor_token(&token));
        assert!(is_local_cursor_authorization(&format!("Bearer {token}")));
        assert!(!is_local_cursor_authorization(&token));
        assert!(!is_local_cursor_authorization(
            "Bearer official-cursor-token"
        ));
        assert!(!is_local_cursor_token("official-cursor-token"));
        assert!(!is_local_cursor_token(""));
    }
}
