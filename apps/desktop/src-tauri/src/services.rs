//! Portico Services from the desktop window. Services answers pages only from the addresses in its
//! own settings, which the desktop app's page is not, so the page's calls to it go through here:
//! only to Services' own address and only its `/v1/` routes. Used for signing in with a Portico
//! Account by a code approved in the browser (the TV apps' code flow).
use serde::{Deserialize, Serialize};

/// Portico Services, as the apps reach it (`hostedUrl` in the native apps).
pub const ORIGIN: &str = "https://web.getportico.tv";

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Request {
    pub method: String,
    pub path: String,
    pub headers: Vec<(String, String)>,
    pub body: Option<String>,
}

#[derive(Serialize)]
pub struct Answer {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: String,
}

/// A Services route this window may call: `/v1/` and nothing that could leave it.
pub fn allowed(request: &Request) -> bool {
    matches!(request.method.as_str(), "GET" | "POST" | "PUT" | "PATCH" | "DELETE")
        && request.path.starts_with("/v1/")
        && !request.path.contains("..")
        && !request.path.contains("//")
        && !request.path.contains('#')
        && !request.path.chars().any(|c| c.is_control() || c == '\\' || c == '@')
}

/// Headers the page may set: the ones Services reads. Cookies and hop-by-hop headers never go.
fn passed(name: &str) -> bool {
    matches!(name.to_ascii_lowercase().as_str(), "authorization" | "content-type" | "idempotency-key" | "accept" | "if-match")
}

pub fn send(request: Request) -> Result<Answer, String> {
    if !allowed(&request) {
        return Err("forbidden".into());
    }
    let agent: ureq::Agent = ureq::Agent::config_builder().http_status_as_error(false).timeout_global(Some(std::time::Duration::from_secs(30))).build().into();
    let url = format!("{ORIGIN}{}", request.path);
    let mut builder = ureq::http::Request::builder().method(request.method.as_str()).uri(url.as_str());
    for (name, value) in request.headers.iter().filter(|(name, _)| passed(name)) {
        builder = builder.header(name.as_str(), value.as_str());
    }
    let built = builder.body(request.body.unwrap_or_default()).map_err(|_| "forbidden")?;
    let mut response = agent.run(built).map_err(|_| "services_unreachable")?;
    let headers = response.headers().iter().filter_map(|(name, value)| Some((name.to_string(), value.to_str().ok()?.to_string()))).collect();
    let status = response.status().as_u16();
    let body = response.body_mut().with_config().limit(1 << 20).read_to_string().map_err(|_| "services_unreachable")?;
    Ok(Answer { status, headers, body })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn request(method: &str, path: &str) -> Request {
        Request { method: method.into(), path: path.into(), headers: vec![], body: None }
    }

    #[test]
    fn only_services_own_v1_routes_are_reached() {
        assert!(allowed(&request("POST", "/v1/tv-codes")));
        assert!(allowed(&request("POST", "/v1/tv-codes/abc/redeem")));
        for (method, path) in [("POST", "/v2/x"), ("POST", "//evil.example/v1/x"), ("GET", "/v1/../admin"), ("GET", "/v1/x@evil.example"),
            ("TRACE", "/v1/x"), ("GET", "https://evil.example/v1/x"), ("GET", "/v1/a\\b")] {
            assert!(!allowed(&request(method, path)), "{method} {path}");
        }
    }

    #[test]
    fn only_the_headers_services_reads_are_sent() {
        assert!(passed("Authorization") && passed("content-type") && passed("Idempotency-Key"));
        assert!(!passed("Cookie") && !passed("Host") && !passed("Origin"));
    }
}
