use url::Url;

use crate::gemini::GeminiError;

/// Parse a gemini:// URL string into a `url::Url`.
/// Ensures scheme is "gemini", default port 1965, default path "/".
pub fn parse_gemini_url(raw: &str) -> Result<Url, GeminiError> {
    let parsed = Url::parse(raw).map_err(|e| GeminiError::InvalidUrl(format!("{raw}: {e}")))?;

    if parsed.scheme() != "gemini" {
        return Err(GeminiError::InvalidUrl(format!(
            "expected gemini:// scheme, got {}://",
            parsed.scheme()
        )));
    }

    if parsed.host_str().is_none() || parsed.host_str() == Some("") {
        return Err(GeminiError::InvalidUrl("missing host in URL".to_string()));
    }

    // For non-special schemes like gemini, url::Url may return an empty path.
    // Normalize to "/" as per the Gemini spec.
    let mut result = parsed;
    if result.path().is_empty() {
        result.set_path("/");
    }

    Ok(result)
}

/// Resolve a potentially-relative URL against a base URL.
/// Handles absolute gemini:// URLs, protocol-relative, and relative paths.
pub fn resolve_url(base: &Url, reference: &str) -> Result<Url, GeminiError> {
    let trimmed = reference.trim();

    // If it's already an absolute gemini:// URL, parse it directly
    if trimmed.starts_with("gemini://") {
        return parse_gemini_url(trimmed);
    }

    // Otherwise resolve relative to base
    let resolved = base
        .join(trimmed)
        .map_err(|e| GeminiError::InvalidUrl(format!("cannot resolve {trimmed}: {e}")))?;

    if resolved.scheme() != "gemini" {
        return Err(GeminiError::InvalidUrl(format!(
            "resolved URL has non-gemini scheme: {}",
            resolved.scheme()
        )));
    }

    Ok(resolved)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_basic_url() {
        let url = parse_gemini_url("gemini://example.com/path").unwrap();
        assert_eq!(url.host_str(), Some("example.com"));
        assert_eq!(url.path(), "/path");
    }

    #[test]
    fn test_parse_default_port() {
        let url = parse_gemini_url("gemini://example.com/").unwrap();
        // url::Url doesn't store port if not explicit, so port() returns None
        // Our code uses unwrap_or(1965) when connecting
        assert!(url.port().is_none() || url.port() == Some(1965));
    }

    #[test]
    fn test_parse_default_path() {
        let url = parse_gemini_url("gemini://example.com").unwrap();
        assert_eq!(url.path(), "/");
    }

    #[test]
    fn test_parse_with_explicit_port() {
        let url = parse_gemini_url("gemini://example.com:1966/").unwrap();
        assert_eq!(url.port(), Some(1966));
    }

    #[test]
    fn test_reject_non_gemini_scheme() {
        let result = parse_gemini_url("https://example.com");
        assert!(result.is_err());
        let err = result.unwrap_err();
        assert!(matches!(err, GeminiError::InvalidUrl(_)));
    }

    #[test]
    fn test_reject_missing_host() {
        let result = parse_gemini_url("gemini:///path");
        assert!(result.is_err());
        let err = result.unwrap_err();
        assert!(matches!(err, GeminiError::InvalidUrl(_)));
    }

    #[test]
    fn test_resolve_absolute_url() {
        let base = Url::parse("gemini://base.com/dir/page").unwrap();
        let resolved = resolve_url(&base, "gemini://other.com/page").unwrap();
        assert_eq!(resolved.host_str(), Some("other.com"));
        assert_eq!(resolved.path(), "/page");
    }

    #[test]
    fn test_resolve_relative_path() {
        let base = Url::parse("gemini://host/dir/page1").unwrap();
        let resolved = resolve_url(&base, "page2").unwrap();
        assert_eq!(resolved.host_str(), Some("host"));
        assert_eq!(resolved.path(), "/dir/page2");
    }

    #[test]
    fn test_resolve_relative_root() {
        let base = Url::parse("gemini://host/dir/page").unwrap();
        let resolved = resolve_url(&base, "/other").unwrap();
        assert_eq!(resolved.host_str(), Some("host"));
        assert_eq!(resolved.path(), "/other");
    }

    #[test]
    fn test_resolve_parent_path() {
        let base = Url::parse("gemini://host/a/b/c").unwrap();
        let resolved = resolve_url(&base, "../up").unwrap();
        assert_eq!(resolved.host_str(), Some("host"));
        assert_eq!(resolved.path(), "/a/up");
    }
}
