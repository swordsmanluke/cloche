use std::io::{self, Read, Write};
use std::net::TcpStream;
use std::sync::Arc;
use std::time::Duration;

use rustls::client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier};
use rustls::pki_types::{CertificateDer, ServerName, UnixTime};
use rustls::{ClientConfig, ClientConnection, DigitallySignedStruct, SignatureScheme, StreamOwned};
use url::Url;

use crate::url_utils;

const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const IO_TIMEOUT: Duration = Duration::from_secs(5);
const MAX_HEADER_LEN: usize = 1026; // 1024 META + 2 for status digits (+ space, but bounded)
const MAX_BODY_SIZE: usize = 5_242_880; // 5 MB

#[derive(thiserror::Error, Debug)]
pub enum GeminiError {
    #[error("invalid URL: {0}")]
    InvalidUrl(String),

    #[error("connection failed: {0}")]
    ConnectionFailed(String),

    #[error("TLS error: {0}")]
    TlsError(String),

    #[error("timeout connecting to server")]
    Timeout,

    #[error("invalid response header: {0}")]
    InvalidResponse(String),

    #[error("response body too large (exceeds 5 MB)")]
    BodyTooLarge,

    #[error("too many redirects")]
    TooManyRedirects,

    #[error("redirect loop detected")]
    RedirectLoop,

    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
}

/// A parsed Gemini response.
pub struct GeminiResponse {
    pub status: u8,
    pub meta: String,
    pub body: Option<Vec<u8>>,
}

/// A ServerCertVerifier that accepts all certificates (skip verification).
#[derive(Debug)]
struct NoVerifier;

impl ServerCertVerifier for NoVerifier {
    fn verify_server_cert(
        &self,
        _end_entity: &CertificateDer<'_>,
        _intermediates: &[CertificateDer<'_>],
        _server_name: &ServerName<'_>,
        _ocsp_response: &[u8],
        _now: UnixTime,
    ) -> Result<ServerCertVerified, rustls::Error> {
        Ok(ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        _message: &[u8],
        _cert: &CertificateDer<'_>,
        _dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        Ok(HandshakeSignatureValid::assertion())
    }

    fn verify_tls13_signature(
        &self,
        _message: &[u8],
        _cert: &CertificateDer<'_>,
        _dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        Ok(HandshakeSignatureValid::assertion())
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        rustls::crypto::ring::default_provider()
            .signature_verification_algorithms
            .supported_schemes()
    }
}

fn build_tls_config() -> Arc<ClientConfig> {
    let config = ClientConfig::builder()
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(NoVerifier))
        .with_no_client_auth();
    Arc::new(config)
}

/// Parse a response header line (without the trailing \r\n) into (status, meta).
pub fn parse_response_header(line: &str) -> Result<(u8, String), GeminiError> {
    let line = line.trim_end_matches('\n').trim_end_matches('\r');

    if line.len() < 2 {
        return Err(GeminiError::InvalidResponse(
            "invalid status code".to_string(),
        ));
    }

    let status_str = &line[..2];

    // Validate both chars are ASCII digits
    if !status_str.bytes().all(|b| b.is_ascii_digit()) {
        return Err(GeminiError::InvalidResponse(
            "invalid status code".to_string(),
        ));
    }

    let status: u8 = status_str
        .parse()
        .map_err(|_| GeminiError::InvalidResponse("invalid status code".to_string()))?;

    if !(10..=69).contains(&status) {
        return Err(GeminiError::InvalidResponse(
            "status code out of range".to_string(),
        ));
    }

    let meta = if line.len() > 2 {
        let rest = &line[2..];
        if let Some(stripped) = rest.strip_prefix(' ') {
            stripped.to_string()
        } else {
            // Characters after status but no space — treat as no meta
            return Err(GeminiError::InvalidResponse(
                "invalid status code".to_string(),
            ));
        }
    } else {
        String::new()
    };

    Ok((status, meta))
}

/// Read the response header from a TLS stream, returning the raw header line.
fn read_header(
    stream: &mut StreamOwned<ClientConnection, TcpStream>,
) -> Result<String, GeminiError> {
    let mut buf = Vec::with_capacity(MAX_HEADER_LEN);
    let mut byte = [0u8; 1];

    loop {
        match stream.read(&mut byte) {
            Ok(0) => {
                if buf.is_empty() {
                    return Err(GeminiError::InvalidResponse("empty response".to_string()));
                }
                // EOF before \r\n — return what we have
                break;
            }
            Ok(_) => {
                buf.push(byte[0]);
                if buf.len() >= 2 && buf[buf.len() - 2] == b'\r' && buf[buf.len() - 1] == b'\n' {
                    // Found \r\n, remove them
                    buf.truncate(buf.len() - 2);
                    break;
                }
                if buf.len() > MAX_HEADER_LEN {
                    return Err(GeminiError::InvalidResponse(
                        "header line too long".to_string(),
                    ));
                }
            }
            Err(e) => return Err(GeminiError::Io(e)),
        }
    }

    String::from_utf8(buf)
        .map_err(|_| GeminiError::InvalidResponse("header is not valid UTF-8".to_string()))
}

/// Read the response body with a size limit.
fn read_body(
    stream: &mut StreamOwned<ClientConnection, TcpStream>,
) -> Result<Vec<u8>, GeminiError> {
    let mut body = Vec::new();
    let mut total = 0usize;
    let mut buf = [0u8; 8192];

    loop {
        match stream.read(&mut buf) {
            Ok(0) => break,
            Ok(n) => {
                total += n;
                if total > MAX_BODY_SIZE {
                    return Err(GeminiError::BodyTooLarge);
                }
                body.extend_from_slice(&buf[..n]);
            }
            Err(e) if e.kind() == io::ErrorKind::ConnectionAborted => break,
            Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => break,
            Err(e) => return Err(GeminiError::Io(e)),
        }
    }

    Ok(body)
}

/// Fetch a Gemini page. Handles the full request/response cycle for a single URL.
/// Does NOT follow redirects — the caller handles redirect logic.
pub fn fetch(url: &Url) -> Result<GeminiResponse, GeminiError> {
    let host = url
        .host_str()
        .ok_or_else(|| GeminiError::InvalidUrl("missing host".to_string()))?;
    let port = url.port().unwrap_or(1965);

    let addr = format!("{host}:{port}");

    // DNS resolve and connect with timeout
    let sock_addrs: Vec<std::net::SocketAddr> = addr
        .to_socket_addrs()
        .map_err(|e| GeminiError::ConnectionFailed(e.to_string()))?
        .collect();

    if sock_addrs.is_empty() {
        return Err(GeminiError::ConnectionFailed(
            "could not resolve host".to_string(),
        ));
    }

    let tcp = connect_with_timeout(&sock_addrs, CONNECT_TIMEOUT)?;
    tcp.set_read_timeout(Some(IO_TIMEOUT))?;
    tcp.set_write_timeout(Some(IO_TIMEOUT))?;

    // TLS handshake
    let tls_config = build_tls_config();
    let server_name =
        ServerName::try_from(host.to_string()).map_err(|e| GeminiError::TlsError(e.to_string()))?;
    let conn = ClientConnection::new(tls_config, server_name)
        .map_err(|e| GeminiError::TlsError(e.to_string()))?;
    let mut tls_stream = StreamOwned::new(conn, tcp);

    // Send request
    let request = format!("{url}\r\n");
    tls_stream
        .write_all(request.as_bytes())
        .map_err(|e| GeminiError::ConnectionFailed(e.to_string()))?;

    // Read and parse header
    let header_line = read_header(&mut tls_stream)?;
    let (status, meta) = parse_response_header(&header_line)?;

    // Validate that 1x and 3x have non-empty meta
    let first_digit = status / 10;
    if (first_digit == 1 || first_digit == 3) && meta.is_empty() {
        return Err(GeminiError::InvalidResponse("missing meta".to_string()));
    }

    // Read body for 2x responses only
    let body = if first_digit == 2 {
        Some(read_body(&mut tls_stream)?)
    } else {
        None
    };

    Ok(GeminiResponse { status, meta, body })
}

/// Try connecting to any of the resolved addresses with a timeout.
fn connect_with_timeout(
    addrs: &[std::net::SocketAddr],
    timeout: Duration,
) -> Result<TcpStream, GeminiError> {
    let mut last_err = None;
    for addr in addrs {
        match TcpStream::connect_timeout(addr, timeout) {
            Ok(stream) => return Ok(stream),
            Err(e) => {
                if e.kind() == io::ErrorKind::TimedOut {
                    return Err(GeminiError::Timeout);
                }
                last_err = Some(e);
            }
        }
    }
    Err(GeminiError::ConnectionFailed(
        last_err
            .map(|e| e.to_string())
            .unwrap_or_else(|| "no addresses to connect to".to_string()),
    ))
}

use std::net::ToSocketAddrs;

/// Check for redirect loops and max hops.
pub fn check_redirect(
    visited: &[String],
    target: &str,
    max_hops: usize,
) -> Result<(), GeminiError> {
    if visited.len() >= max_hops {
        return Err(GeminiError::TooManyRedirects);
    }
    if visited.iter().any(|u| u == target) {
        return Err(GeminiError::RedirectLoop);
    }
    Ok(())
}

/// Navigate to a URL, following redirects. Returns the final response and the final URL.
pub fn fetch_with_redirects(start_url: &Url) -> Result<(GeminiResponse, Url), GeminiError> {
    let mut current_url = start_url.clone();
    let mut visited: Vec<String> = vec![current_url.to_string()];
    let max_hops = 5;

    loop {
        let response = fetch(&current_url)?;
        let first_digit = response.status / 10;

        if first_digit == 3 {
            let target = url_utils::resolve_url(&current_url, &response.meta)?;
            let target_str = target.to_string();
            check_redirect(&visited, &target_str, max_hops)?;
            visited.push(target_str);
            current_url = target;
        } else {
            return Ok((response, current_url));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_success_header() {
        let (status, meta) = parse_response_header("20 text/gemini").unwrap();
        assert_eq!(status, 20);
        assert_eq!(meta, "text/gemini");
    }

    #[test]
    fn test_parse_input_header() {
        let (status, meta) = parse_response_header("10 Enter your name").unwrap();
        assert_eq!(status, 10);
        assert_eq!(meta, "Enter your name");
    }

    #[test]
    fn test_parse_redirect_header() {
        let (status, meta) = parse_response_header("31 gemini://other/path").unwrap();
        assert_eq!(status, 31);
        assert_eq!(meta, "gemini://other/path");
    }

    #[test]
    fn test_parse_temp_failure() {
        let (status, meta) = parse_response_header("40 Server busy").unwrap();
        assert_eq!(status, 40);
        assert_eq!(meta, "Server busy");
    }

    #[test]
    fn test_parse_perm_failure() {
        let (status, meta) = parse_response_header("51 Not found").unwrap();
        assert_eq!(status, 51);
        assert_eq!(meta, "Not found");
    }

    #[test]
    fn test_parse_cert_required() {
        let (status, meta) = parse_response_header("60 Certificate required").unwrap();
        assert_eq!(status, 60);
        assert_eq!(meta, "Certificate required");
    }

    #[test]
    fn test_parse_empty_meta() {
        let (status, meta) = parse_response_header("20 ").unwrap();
        assert_eq!(status, 20);
        assert_eq!(meta, "");
    }

    #[test]
    fn test_parse_meta_no_space() {
        let (status, meta) = parse_response_header("20").unwrap();
        assert_eq!(status, 20);
        assert_eq!(meta, "");
    }

    #[test]
    fn test_reject_single_digit() {
        let result = parse_response_header("2 text");
        assert!(result.is_err());
        let err = result.unwrap_err();
        assert!(matches!(err, GeminiError::InvalidResponse(_)));
    }

    #[test]
    fn test_reject_non_numeric() {
        let result = parse_response_header("AB text");
        assert!(result.is_err());
        let err = result.unwrap_err();
        assert!(matches!(err, GeminiError::InvalidResponse(_)));
    }

    #[test]
    fn test_reject_status_out_of_range() {
        let result = parse_response_header("70 whatever");
        assert!(result.is_err());
        let err = result.unwrap_err();
        assert!(matches!(err, GeminiError::InvalidResponse(_)));
    }

    #[test]
    fn test_reject_status_too_low() {
        let result = parse_response_header("09 whatever");
        assert!(result.is_err());
    }

    #[test]
    fn test_redirect_loop_detection() {
        let visited = vec![
            "gemini://example.com/a".to_string(),
            "gemini://example.com/b".to_string(),
        ];
        let result = check_redirect(&visited, "gemini://example.com/a", 5);
        assert!(matches!(result, Err(GeminiError::RedirectLoop)));
    }

    #[test]
    fn test_redirect_max_hops() {
        let visited: Vec<String> = (0..5)
            .map(|i| format!("gemini://example.com/{i}"))
            .collect();
        let result = check_redirect(&visited, "gemini://example.com/new", 5);
        assert!(matches!(result, Err(GeminiError::TooManyRedirects)));
    }

    #[test]
    fn test_redirect_ok() {
        let visited = vec!["gemini://example.com/a".to_string()];
        let result = check_redirect(&visited, "gemini://example.com/b", 5);
        assert!(result.is_ok());
    }
}
