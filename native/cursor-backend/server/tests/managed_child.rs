//! A real child in a disposable user profile. Never initializes or trusts a CA.
use reqwest::{blocking::Client, StatusCode};
use std::{
    fs,
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

struct OwnedChild(Child);
impl Drop for OwnedChild {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

#[test]
fn managed_child_is_private_and_startup_shutdown_leave_cursor_untouched() {
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path();
    let home = root.join("home");
    let roaming = home.join("AppData/Roaming");
    let xdg = home.join(".config");
    let data = root.join("managed");
    fs::create_dir_all(&data).unwrap();
    for settings in [
        roaming.join("Cursor/User/settings.json"),
        xdg.join("Cursor/User/settings.json"),
        home.join("Library/Application Support/Cursor/User/settings.json"),
    ] {
        fs::create_dir_all(settings.parent().unwrap()).unwrap();
        fs::write(
            settings,
            b"{\"http.proxy\":\"http://company-proxy:80\",\"editor.fontSize\":16}",
        )
        .unwrap();
    }
    let token = "a".repeat(64);
    let token_path = root.join("control.token");
    let ready_path = root.join("ready.json");
    fs::write(&token_path, &token).unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&token_path, fs::Permissions::from_mode(0o600)).unwrap();
    }
    let executable = env!("CARGO_BIN_EXE_magpie-cursor-backend");
    let mut command = Command::new(executable);
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        // Match Magpie's hidden child launch, without a console control handler.
        command.creation_flags(0x08000000);
    }
    let mut child = OwnedChild(
        command
            .args([
                "--data-dir",
                data.to_str().unwrap(),
                "--control-token-file",
                token_path.to_str().unwrap(),
                "--ready-file",
                ready_path.to_str().unwrap(),
            ])
            .env("HOME", &home)
            .env("USERPROFILE", &home)
            .env("APPDATA", &roaming)
            .env("LOCALAPPDATA", home.join("AppData/Local"))
            .env("XDG_CONFIG_HOME", &xdg)
            .env("XDG_CACHE_HOME", home.join(".cache"))
            .env_remove("CURSOR_DATABASE_URL")
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap(),
    );
    let deadline = Instant::now() + Duration::from_secs(20);
    let ready: serde_json::Value = loop {
        if let Ok(bytes) = fs::read(&ready_path) {
            break serde_json::from_slice(&bytes).unwrap();
        }
        assert!(
            child.0.try_wait().unwrap().is_none(),
            "backend exited before ready"
        );
        assert!(Instant::now() < deadline, "backend readiness timed out");
        std::thread::sleep(Duration::from_millis(10));
    };
    assert_eq!(ready["pid"], child.0.id());
    let base = format!("http://127.0.0.1:{}", ready["port"].as_u64().unwrap());
    let client = Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(3))
        .build()
        .unwrap();
    let models = format!("{base}/__byok-api__/api/models");
    assert_eq!(
        client.get(&models).send().unwrap().status(),
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        client
            .get(&models)
            .bearer_auth(&token)
            .header("Origin", "null")
            .send()
            .unwrap()
            .status(),
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        client
            .get(format!("{base}/__byok-api__/api/plugins"))
            .bearer_auth(&token)
            .send()
            .unwrap()
            .status(),
        StatusCode::NOT_FOUND
    );
    assert_eq!(
        client
            .get(format!("{base}/__byok-api__/healthz"))
            .bearer_auth(&token)
            .send()
            .unwrap()
            .status(),
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        client
            .get(&models)
            .bearer_auth(&token)
            .send()
            .unwrap()
            .json::<serde_json::Value>()
            .unwrap(),
        serde_json::json!([])
    );
    let input = serde_json::json!({"display_name":"Smoke Model", "type":"openai", "base_url":"http://127.0.0.1:9", "api_key":"sandbox-only", "tooltip_data":"Managed by Magpie", "model_id":"smoke", "openai_endpoint":"/v1/chat/completions"});
    let created: serde_json::Value = client
        .post(&models)
        .bearer_auth(&token)
        .json(&serde_json::json!({"models":[input.clone()]}))
        .send()
        .unwrap()
        .error_for_status()
        .unwrap()
        .json()
        .unwrap();
    let item = format!("{models}/{}", created[0]["model_hash"].as_str().unwrap());
    assert_eq!(
        client
            .put(&item)
            .bearer_auth(&token)
            .json(&input)
            .send()
            .unwrap()
            .status(),
        StatusCode::OK
    );
    assert_eq!(
        client
            .delete(&item)
            .bearer_auth(&token)
            .send()
            .unwrap()
            .status(),
        StatusCode::NO_CONTENT
    );
    for _ in 0..2 {
        let status: serde_json::Value = client
            .get(format!("{base}/__byok-api__/api/harness/cursor/status"))
            .bearer_auth(&token)
            .send()
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .unwrap();
        assert_eq!(status["ca"], "missing");
        assert_eq!(status["integration"], "disabled");
    }
    assert!(!data.join("ca").exists());
    assert!(data.join("cursor-byok.db").is_file());
    drop(child.0.stdin.take());
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        if let Some(status) = child.0.try_wait().unwrap() {
            assert!(status.success());
            break;
        }
        assert!(
            Instant::now() < deadline,
            "backend did not exit after parent EOF"
        );
        std::thread::sleep(Duration::from_millis(10));
    }
    assert!(!ready_path.exists());
    assert!(client.get(&models).send().is_err());
    assert!(!home.join(".cursor-byok-v3").exists());
    for settings in [
        roaming.join("Cursor/User/settings.json"),
        xdg.join("Cursor/User/settings.json"),
        home.join("Library/Application Support/Cursor/User/settings.json"),
    ] {
        assert_eq!(
            fs::read(&settings).unwrap(),
            b"{\"http.proxy\":\"http://company-proxy:80\",\"editor.fontSize\":16}"
        );
        assert!(!settings
            .parent()
            .unwrap()
            .join("globalStorage/state.vscdb")
            .exists());
    }
}
