//! Magpie's private child-process boundary. Cursor wire traffic retains its protocol.
use axum::{
    extract::{Request, State},
    http::{header, Method, StatusCode},
    middleware::Next,
    response::{IntoResponse, Response},
};
use sha2::{Digest, Sha256};

pub(crate) const RELAY_TOKEN_HEADER: &str = "x-magpie-cursor-relay";

pub(crate) fn relay_token(control_token: &str) -> String {
    hex::encode(Sha256::digest(format!(
        "magpie-cursor-relay-v1\0{control_token}"
    )))
}

pub async fn authorize(State(token): State<String>, mut request: Request, next: Next) -> Response {
    // Consume the private relay header before any transport, trace or upstream sees it.
    let from_relay = request
        .headers_mut()
        .remove(RELAY_TOKEN_HEADER)
        .is_some_and(|value| constant_time_equal(value.as_bytes(), relay_token(&token).as_bytes()));
    let is_control = request.uri().path().starts_with("/__byok-api__");
    // The native Cursor proxy may forward a renderer Origin. Only requests it
    // authenticated can retain that header; control is always server-to-server.
    if request.headers().contains_key(header::ORIGIN) && (is_control || !from_relay) {
        return StatusCode::FORBIDDEN.into_response();
    }
    let path = request.uri().path();
    if is_control {
        let valid = request
            .headers()
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.strip_prefix("Bearer "))
            .is_some_and(|value| constant_time_equal(value.as_bytes(), token.as_bytes()));
        if !valid {
            return StatusCode::UNAUTHORIZED.into_response();
        }
        if !allowed_control(request.method(), path) {
            return StatusCode::NOT_FOUND.into_response();
        }
    }
    next.run(request).await
}

fn constant_time_equal(left: &[u8], right: &[u8]) -> bool {
    if left.len() != right.len() {
        return false;
    }
    left.iter()
        .zip(right)
        .fold(0_u8, |difference, (a, b)| difference | (a ^ b))
        == 0
}

fn allowed_control(method: &Method, path: &str) -> bool {
    match path {
        "/__byok-api__/healthz" | "/__byok-api__/api/harness/cursor/status" => {
            method == Method::GET
        }
        "/__byok-api__/api/models" => method == Method::GET || method == Method::POST,
        "/__byok-api__/api/harness/cursor/ca/initialize"
        | "/__byok-api__/api/harness/cursor/ca/trust" => method == Method::POST,
        "/__byok-api__/api/harness/cursor/enabled" => method == Method::PUT,
        _ => path
            .strip_prefix("/__byok-api__/api/models/")
            .is_some_and(|hash| {
                hash.len() == 16
                    && hash.bytes().all(|value| value.is_ascii_hexdigit())
                    && (method == Method::PUT || method == Method::DELETE)
            }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::Body, routing::get, Router};
    use tower::ServiceExt;

    fn app() -> Router {
        Router::new()
            .route(
                "/__byok-api__/healthz",
                get(|| async { StatusCode::NO_CONTENT }),
            )
            .fallback(|request: Request| async move {
                assert!(!request.headers().contains_key(RELAY_TOKEN_HEADER));
                StatusCode::OK
            })
            .layer(axum::middleware::from_fn_with_state(
                "private-token".to_owned(),
                authorize,
            ))
    }

    #[tokio::test]
    async fn managed_relay_allows_cursor_origin_only_and_consumes_secret() {
        let relay = relay_token("private-token");
        for (path, token, origin, status) in [
            (
                "/agent.v1.AgentService/RunSSE",
                relay.as_str(),
                "vscode-file://vscode-app",
                StatusCode::OK,
            ),
            (
                "/agent.v1.AgentService/RunSSE",
                relay.as_str(),
                "null",
                StatusCode::OK,
            ),
            (
                "/agent.v1.AgentService/RunSSE",
                "spoof",
                "vscode-file://vscode-app",
                StatusCode::FORBIDDEN,
            ),
            (
                "/__byok-api__/healthz",
                relay.as_str(),
                "vscode-file://vscode-app",
                StatusCode::FORBIDDEN,
            ),
        ] {
            let request = Request::builder()
                .uri(path)
                .header(RELAY_TOKEN_HEADER, token)
                .header(header::ORIGIN, origin)
                .header(header::AUTHORIZATION, "Bearer private-token")
                .body(Body::empty())
                .unwrap();
            assert_eq!(app().oneshot(request).await.unwrap().status(), status);
        }
    }

    #[test]
    fn control_allowlist_uses_upstreams_sixteen_digit_model_hash() {
        assert!(allowed_control(
            &Method::PUT,
            "/__byok-api__/api/models/0123456789abcdef"
        ));
        assert!(allowed_control(
            &Method::DELETE,
            "/__byok-api__/api/models/0123456789abcdef"
        ));
        assert!(!allowed_control(
            &Method::GET,
            "/__byok-api__/api/models/0123456789abcdef"
        ));
        assert!(!allowed_control(
            &Method::POST,
            "/__byok-api__/api/models/discover"
        ));
        assert!(!allowed_control(&Method::GET, "/__byok-api__/api/plugins"));
    }

    #[tokio::test]
    async fn rejects_browser_and_unauthenticated_control_before_dispatch() {
        for (path, token, origin, status) in [
            (
                "/__byok-api__/healthz",
                None,
                None,
                StatusCode::UNAUTHORIZED,
            ),
            (
                "/__byok-api__/healthz",
                Some("wrong"),
                None,
                StatusCode::UNAUTHORIZED,
            ),
            (
                "/__byok-api__/healthz",
                Some("private-token"),
                Some("null"),
                StatusCode::FORBIDDEN,
            ),
            (
                "/__byok-api__/healthz",
                Some("private-token"),
                Some("http://127.0.0.1"),
                StatusCode::FORBIDDEN,
            ),
            (
                "/__byok-api__/healthz",
                Some("private-token"),
                None,
                StatusCode::NO_CONTENT,
            ),
            (
                "/__byok-api__/api/promotions",
                Some("private-token"),
                None,
                StatusCode::NOT_FOUND,
            ),
            (
                "/__byok-api__/api/plugins",
                Some("private-token"),
                None,
                StatusCode::NOT_FOUND,
            ),
            (
                "/agent.v1.AgentService/RunSSE",
                None,
                Some("https://hostile.example"),
                StatusCode::FORBIDDEN,
            ),
        ] {
            let mut request = Request::builder().uri(path);
            if let Some(token) = token {
                request = request.header(header::AUTHORIZATION, format!("Bearer {token}"));
            }
            if let Some(origin) = origin {
                request = request.header(header::ORIGIN, origin);
            }
            assert_eq!(
                app()
                    .oneshot(request.body(Body::empty()).unwrap())
                    .await
                    .unwrap()
                    .status(),
                status,
                "{path}"
            );
        }
    }
}
