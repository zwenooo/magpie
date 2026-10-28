//! Keeps the local Cursor identity local when the editor forces a token refresh.
use axum::{
    body::Body,
    extract::Extension,
    http::{header, HeaderValue, Request, Response},
};
use futures_util::{stream, StreamExt};

use crate::{
    api::cursor::proxy::{self, CursorProxy},
    local_app, Error, Result,
};

pub const PATH: &str = "/oauth/token";
const MAX_LOCAL_REFRESH_BODY: usize = 64 * 1024;

pub async fn refresh(
    Extension(upstream): Extension<CursorProxy>,
    request: Request<Body>,
) -> Result<Response<Body>> {
    if request
        .headers()
        .get_all(header::CONTENT_ENCODING)
        .iter()
        .any(|value| {
            !value
                .to_str()
                .is_ok_and(|value| value.trim().eq_ignore_ascii_case("identity"))
        })
    {
        return proxy::forward(Extension(upstream), request).await;
    }
    let (parts, body) = request.into_parts();
    let mut body = body.into_data_stream();
    let mut chunks = Vec::new();
    let mut length: usize = 0;
    while let Some(chunk) = body.next().await {
        let chunk = chunk
            .map_err(|error| Error::Protocol(format!("cannot read token refresh body: {error}")))?;
        length = length.saturating_add(chunk.len());
        chunks.push(chunk);
        if length > MAX_LOCAL_REFRESH_BODY {
            // Limit local parsing, not the official endpoint. Replay every
            // consumed byte and let an oversized request continue unchanged.
            let prefix = stream::iter(chunks.into_iter().map(Ok::<_, axum::Error>));
            let request = Request::from_parts(parts, Body::from_stream(prefix.chain(body)));
            return proxy::forward(Extension(upstream), request).await;
        }
    }
    let mut bytes = Vec::with_capacity(length);
    for chunk in chunks {
        bytes.extend_from_slice(&chunk);
    }
    let local = local_app::local_cursor_token()?;
    let local_refresh = serde_json::from_slice::<serde_json::Value>(&bytes)
        .ok()
        .is_some_and(|value| {
            value.get("grant_type").and_then(|value| value.as_str()) == Some("refresh_token")
                && value.get("refresh_token").and_then(|value| value.as_str())
                    == Some(local.as_str())
        });
    if !local_refresh {
        return proxy::forward(
            Extension(upstream),
            Request::from_parts(parts, Body::from(bytes)),
        )
        .await;
    }
    // Cursor's cursor-session-refresh.ts reads access_token and stores that
    // value as both access and refresh tokens. No official session is minted.
    let bytes = serde_json::to_vec(&serde_json::json!({ "access_token": local }))?;
    let mut response = Response::new(Body::from(bytes));
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    // Match the local Stripe endpoint for renderer-origin requests. The
    // managed outer boundary accepts these origins only from our own proxy.
    if let Some(origin) = parts.headers.get(header::ORIGIN) {
        response
            .headers_mut()
            .insert(header::ACCESS_CONTROL_ALLOW_ORIGIN, origin.clone());
        response.headers_mut().insert(
            header::ACCESS_CONTROL_ALLOW_CREDENTIALS,
            HeaderValue::from_static("true"),
        );
        response
            .headers_mut()
            .insert(header::VARY, HeaderValue::from_static("Origin"));
    }
    Ok(response)
}

#[cfg(test)]
mod tests {
    use std::sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    };

    use axum::{body::to_bytes, http::StatusCode, routing::post, Router};
    use tokio::sync::Mutex;
    use tower::ServiceExt;

    use super::*;
    use crate::{
        network::NetworkClients,
        store::{ProxyMode, ProxySettingsInput, Store},
    };

    struct Fixture {
        _directory: tempfile::TempDir,
        store: Store,
        router: Router,
        calls: Arc<AtomicUsize>,
        body: Arc<Mutex<Vec<u8>>>,
        headers: Arc<Mutex<axum::http::HeaderMap>>,
        task: tokio::task::JoinHandle<()>,
    }

    impl Drop for Fixture {
        fn drop(&mut self) {
            self.task.abort();
        }
    }

    impl Fixture {
        async fn new() -> Self {
            let directory = tempfile::tempdir().unwrap();
            let store = Store::connect(&format!(
                "sqlite://{}",
                directory.path().join("refresh.db").display()
            ))
            .await
            .unwrap();
            store
                .set_proxy_settings(ProxySettingsInput {
                    mode: ProxyMode::Custom,
                    address: "http://127.0.0.1:9".into(),
                    ..Default::default()
                })
                .await
                .unwrap();
            let calls = Arc::new(AtomicUsize::new(0));
            let body = Arc::new(Mutex::new(Vec::new()));
            let headers = Arc::new(Mutex::new(axum::http::HeaderMap::new()));
            let observed_calls = calls.clone();
            let observed_body = body.clone();
            let observed_headers = headers.clone();
            let official = Router::new().route(
                PATH,
                post(move |request: Request<Body>| {
                    let calls = observed_calls.clone();
                    let body = observed_body.clone();
                    let headers = observed_headers.clone();
                    async move {
                        calls.fetch_add(1, Ordering::SeqCst);
                        assert_eq!(request.headers()["x-test-refresh"], "preserve");
                        *headers.lock().await = request.headers().clone();
                        *body.lock().await = to_bytes(request.into_body(), usize::MAX)
                            .await
                            .unwrap()
                            .to_vec();
                        Response::builder()
                            .status(StatusCode::UNAUTHORIZED)
                            .header(header::CONTENT_TYPE, "application/json")
                            .header("x-test-upstream", "preserve")
                            .body(Body::from(
                                r#"{"shouldLogout":true,"error":"test-invalid-session"}"#,
                            ))
                            .unwrap()
                    }
                }),
            );
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let url = format!("http://{}", listener.local_addr().unwrap());
            let task = tokio::spawn(async move { axum::serve(listener, official).await.unwrap() });
            let router =
                Router::new()
                    .route(PATH, post(refresh))
                    .layer(Extension(CursorProxy::for_test(
                        NetworkClients::new(store.clone()),
                        url,
                    )));
            Self {
                _directory: directory,
                store,
                router,
                calls,
                body,
                headers,
                task,
            }
        }

        async fn stop(&mut self) {
            self.task.abort();
            let _ = (&mut self.task).await;
            self.store.pool().close().await;
        }

        async fn request(&self, body: Vec<u8>) -> Response<Body> {
            self.router
                .clone()
                .oneshot(
                    Request::post(PATH)
                        .header(header::CONTENT_TYPE, "application/json")
                        .header(header::ORIGIN, "vscode-file://vscode-app")
                        .header("x-test-refresh", "preserve")
                        .body(Body::from(body))
                        .unwrap(),
                )
                .await
                .unwrap()
        }
    }

    #[tokio::test]
    async fn local_refresh_never_reaches_official_upstream() {
        let mut fixture = Fixture::new().await;
        let local = local_app::local_cursor_token().unwrap();
        let response = fixture.request(serde_json::to_vec(&serde_json::json!({
            "grant_type": "refresh_token", "client_id": "fixture-desktop", "refresh_token": local,
        })).unwrap()).await;
        assert_eq!(
            response.status(),
            StatusCode::OK,
            "local refresh must not be rejected by the official service"
        );
        assert_eq!(fixture.calls.load(Ordering::SeqCst), 0);
        assert_eq!(response.headers()[header::CONTENT_TYPE], "application/json");
        assert_eq!(
            response.headers()[header::ACCESS_CONTROL_ALLOW_ORIGIN],
            "vscode-file://vscode-app"
        );
        let body = to_bytes(response.into_body(), usize::MAX).await.unwrap();
        let value: serde_json::Value = serde_json::from_slice(&body).unwrap();
        assert!(value["access_token"]
            .as_str()
            .is_some_and(|value| value == local));
        assert_ne!(value["shouldLogout"], serde_json::json!(true));
        fixture.stop().await;
    }

    #[tokio::test]
    async fn other_refresh_requests_and_replies_stay_opaque() {
        let mut fixture = Fixture::new().await;
        let local = local_app::local_cursor_token().unwrap();
        let requests = [
            br#"{ "grant_type": "refresh_token", "refresh_token": "official-fixture", "extra": [1,2] }"#.to_vec(),
            serde_json::to_vec(&serde_json::json!({"grant_type":"authorization_code", "refresh_token":local})).unwrap(),
            serde_json::to_vec(&serde_json::json!({"grant_type":"refresh_token", "refresh_token":format!("{local}-different-signature")})).unwrap(),
            br#"{"grant_type":"refresh_token","refresh_token":null}"#.to_vec(),
            b"opaque-invalid-json".to_vec(),
        ];
        for (index, body) in requests.into_iter().enumerate() {
            let response = fixture.request(body.clone()).await;
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
            assert_eq!(response.headers()["x-test-upstream"], "preserve");
            assert_eq!(fixture.calls.load(Ordering::SeqCst), index + 1);
            assert!(
                fixture.body.lock().await.as_slice() == body,
                "forwarded refresh body changed"
            );
            let response = to_bytes(response.into_body(), usize::MAX).await.unwrap();
            assert!(
                response.as_ref() == br#"{"shouldLogout":true,"error":"test-invalid-session"}"#
            );
        }
        fixture.stop().await;
    }

    #[tokio::test]
    async fn oversized_body_is_forwarded_without_local_interpretation() {
        let mut fixture = Fixture::new().await;
        let local = local_app::local_cursor_token().unwrap();
        let body = serde_json::to_vec(&serde_json::json!({
            "grant_type":"refresh_token", "refresh_token":local, "padding":"x".repeat(64 * 1024),
        }))
        .unwrap();
        // Split across chunks to also prove the parsed prefix is replayed.
        let chunks = body
            .chunks(4096)
            .map(|chunk| {
                Ok::<_, std::convert::Infallible>(axum::body::Bytes::copy_from_slice(chunk))
            })
            .collect::<Vec<_>>();
        let response = fixture
            .router
            .clone()
            .oneshot(
                Request::post(PATH)
                    .header(header::CONTENT_TYPE, "application/json")
                    .header("x-test-refresh", "preserve")
                    .body(Body::from_stream(futures_util::stream::iter(chunks)))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
        assert!(
            fixture.body.lock().await.as_slice() == body,
            "oversized refresh body changed"
        );
        fixture.stop().await;
    }

    #[tokio::test]
    async fn encoded_refresh_keeps_its_headers_and_body_bytes() {
        use std::io::Write;
        let mut fixture = Fixture::new().await;
        let local = local_app::local_cursor_token().unwrap();
        let json = serde_json::to_vec(&serde_json::json!({
            "grant_type":"refresh_token", "refresh_token":local,
        }))
        .unwrap();
        let mut encoder = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
        encoder.write_all(&json).unwrap();
        let compressed = encoder.finish().unwrap();
        // Even a misleading encoding header must not make the handler parse
        // raw JSON as a local refresh; let the official endpoint judge it.
        for body in [compressed, json] {
            let response = fixture
                .router
                .clone()
                .oneshot(
                    Request::post(PATH)
                        .header(header::CONTENT_TYPE, "application/json")
                        .header(header::CONTENT_ENCODING, "gzip")
                        .header(header::CONTENT_LENGTH, body.len())
                        .header("x-test-refresh", "preserve")
                        .body(Body::from(body.clone()))
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
            assert!(
                fixture.body.lock().await.as_slice() == body,
                "encoded refresh body changed"
            );
            let headers = fixture.headers.lock().await;
            assert_eq!(headers[header::CONTENT_ENCODING], "gzip");
            assert_eq!(headers[header::CONTENT_TYPE], "application/json");
            assert_eq!(headers[header::CONTENT_LENGTH], body.len().to_string());
        }
        assert_eq!(fixture.calls.load(Ordering::SeqCst), 2);
        fixture.stop().await;
    }
}
