//! Private Magpie child. It only installs Cursor integration on authenticated API calls.
#![cfg_attr(target_os = "windows", windows_subsystem = "windows")]
use cursor_server::{config, App, Config, Error, Result};
use std::{io::Read, path::PathBuf};
use tokio_util::sync::CancellationToken;
use tracing_subscriber::prelude::*;

#[tokio::main]
async fn main() -> Result<()> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args == ["--version"] {
        println!("magpie-cursor-backend 31e9f5c");
        return Ok(());
    }
    if args == ["--license"] {
        println!("{}", include_str!("../../../LICENSE"));
        return Ok(());
    }
    let data = argument(&args, "--data-dir")?;
    let token_path = argument(&args, "--control-token-file")?;
    let ready = argument(&args, "--ready-file")?;
    if args.len() != 6 {
        return Err(Error::Config(
            "expected --data-dir, --control-token-file and --ready-file".into(),
        ));
    }
    private_file(&token_path)?;
    let token = std::fs::read_to_string(&token_path)?.trim().to_owned();
    if token.len() != 64 || !token.bytes().all(|value| value.is_ascii_hexdigit()) {
        return Err(Error::Config(
            "control token must be 32 random bytes encoded as hex".into(),
        ));
    }
    config::set_managed_data_dir(data.clone())?;
    // Semble is a separate crate; pin its cache before constructing any worker.
    std::env::set_var("SEMBLE_CACHE_LOCATION", data.join("cache/semble"));
    tracing_subscriber::registry()
        .with(tracing_subscriber::EnvFilter::new("cursor_server=info"))
        .with(tracing_subscriber::fmt::layer().with_writer(std::io::stderr))
        .init();
    let app = App::new(Config::magpie()?).await?.protect_managed(token);
    let listener = app.bind().await?;
    let ready_data = serde_json::to_vec(&serde_json::json!({
        "port": listener.local_addr()?.port(), "pid": std::process::id(),
    }))?;
    write_private(&ready, &ready_data)?;
    let shutdown = CancellationToken::new();
    let stdin_shutdown = shutdown.clone();
    // A detached blocking thread avoids tokio waiting forever for stdin on signal shutdown.
    std::thread::spawn(move || {
        let mut input = std::io::stdin().lock();
        let mut buffer = [0_u8; 256];
        loop {
            match input.read(&mut buffer) {
                Ok(0) | Err(_) => {
                    stdin_shutdown.cancel();
                    break;
                }
                Ok(_) => {}
            }
        }
    });
    let signal_shutdown = shutdown.clone();
    let signal = tokio::spawn(async move {
        #[cfg(unix)]
        {
            let mut terminate =
                tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                    .expect("SIGTERM handler");
            tokio::select! { _ = ctrl_c_signal() => {}, _ = terminate.recv() => {} }
        }
        #[cfg(not(unix))]
        ctrl_c_signal().await;
        signal_shutdown.cancel();
    });
    let result = app.serve_on(listener, shutdown).await;
    signal.abort();
    let _ = std::fs::remove_file(ready);
    result
}

async fn ctrl_c_signal() {
    if tokio::signal::ctrl_c().await.is_err() {
        // Detached Windows GUI children may not have a console. An unavailable
        // signal is not a shutdown request; the parent still owns the stdin pipe.
        std::future::pending::<()>().await;
    }
}

fn argument(args: &[String], name: &str) -> Result<PathBuf> {
    let value = args
        .chunks_exact(2)
        .find(|pair| pair[0] == name)
        .map(|pair| PathBuf::from(&pair[1]))
        .ok_or_else(|| Error::Config(format!("missing {name}")))?;
    if !value.is_absolute() {
        return Err(Error::Config(format!("{name} must be absolute")));
    }
    Ok(value)
}

fn private_file(path: &std::path::Path) -> Result<()> {
    let metadata = std::fs::symlink_metadata(path)?;
    if !metadata.file_type().is_file() {
        return Err(Error::Config("control token must be a regular file".into()));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if metadata.permissions().mode() & 0o077 != 0 {
            return Err(Error::Config("control token must be owner-only".into()));
        }
    }
    Ok(())
}

fn write_private(path: &std::path::Path, bytes: &[u8]) -> Result<()> {
    use std::io::Write;
    let temporary = path.with_extension("tmp");
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&temporary)?;
    file.write_all(bytes)?;
    file.sync_all()?;
    std::fs::rename(temporary, path)?;
    Ok(())
}
