/// A single parsed line of text/gemini content.
#[derive(Debug, PartialEq)]
pub enum GeminiLine {
    Text(String),
    Link { url: String, label: String },
    Heading { level: u8, text: String },
    ListItem(String),
    Quote(String),
    PreformattedToggle { alt_text: String },
    PreformattedText(String),
}

/// Parse a text/gemini document body into a Vec<GeminiLine>.
/// Tracks preformatted toggle state: lines between ``` markers become
/// PreformattedText; the ``` lines themselves become PreformattedToggle.
pub fn parse_gemini(body: &str) -> Vec<GeminiLine> {
    let mut lines = Vec::new();
    let mut in_preformatted = false;

    for raw_line in body.lines() {
        if let Some(after_toggle) = raw_line.strip_prefix("```") {
            let alt_text = after_toggle.trim().to_string();
            lines.push(GeminiLine::PreformattedToggle { alt_text });
            in_preformatted = !in_preformatted;
            continue;
        }

        if in_preformatted {
            lines.push(GeminiLine::PreformattedText(raw_line.to_string()));
            continue;
        }

        if let Some(rest) = raw_line.strip_prefix("=>") {
            let rest = rest.trim_start();
            if rest.is_empty() {
                lines.push(GeminiLine::Link {
                    url: String::new(),
                    label: String::new(),
                });
                continue;
            }
            // Split at first whitespace to get URL and optional label
            let (url, label) = match rest.find(|c: char| c.is_whitespace()) {
                Some(pos) => {
                    let url = &rest[..pos];
                    let label = rest[pos..].trim().to_string();
                    (url.to_string(), label)
                }
                None => (rest.to_string(), rest.to_string()),
            };
            lines.push(GeminiLine::Link { url, label });
        } else if let Some(rest) = raw_line.strip_prefix("### ") {
            lines.push(GeminiLine::Heading {
                level: 3,
                text: rest.to_string(),
            });
        } else if let Some(rest) = raw_line.strip_prefix("## ") {
            lines.push(GeminiLine::Heading {
                level: 2,
                text: rest.to_string(),
            });
        } else if let Some(rest) = raw_line.strip_prefix("# ") {
            lines.push(GeminiLine::Heading {
                level: 1,
                text: rest.to_string(),
            });
        } else if let Some(rest) = raw_line.strip_prefix("* ") {
            lines.push(GeminiLine::ListItem(rest.to_string()));
        } else if let Some(rest) = raw_line.strip_prefix("> ") {
            lines.push(GeminiLine::Quote(rest.to_string()));
        } else if let Some(rest) = raw_line.strip_prefix(">") {
            // Quote with no space after >
            lines.push(GeminiLine::Quote(rest.to_string()));
        } else {
            lines.push(GeminiLine::Text(raw_line.to_string()));
        }
    }

    lines
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_text_line() {
        let lines = parse_gemini("Hello, world!");
        assert_eq!(lines, vec![GeminiLine::Text("Hello, world!".to_string())]);
    }

    #[test]
    fn test_link_with_label() {
        let lines = parse_gemini("=> gemini://x/y Link text");
        assert_eq!(
            lines,
            vec![GeminiLine::Link {
                url: "gemini://x/y".to_string(),
                label: "Link text".to_string(),
            }]
        );
    }

    #[test]
    fn test_link_without_label() {
        let lines = parse_gemini("=> gemini://x/y");
        assert_eq!(
            lines,
            vec![GeminiLine::Link {
                url: "gemini://x/y".to_string(),
                label: "gemini://x/y".to_string(),
            }]
        );
    }

    #[test]
    fn test_link_extra_whitespace() {
        let lines = parse_gemini("=>   gemini://x   some label");
        assert_eq!(
            lines,
            vec![GeminiLine::Link {
                url: "gemini://x".to_string(),
                label: "some label".to_string(),
            }]
        );
    }

    #[test]
    fn test_heading_h1() {
        let lines = parse_gemini("# Title");
        assert_eq!(
            lines,
            vec![GeminiLine::Heading {
                level: 1,
                text: "Title".to_string(),
            }]
        );
    }

    #[test]
    fn test_heading_h2() {
        let lines = parse_gemini("## Subtitle");
        assert_eq!(
            lines,
            vec![GeminiLine::Heading {
                level: 2,
                text: "Subtitle".to_string(),
            }]
        );
    }

    #[test]
    fn test_heading_h3() {
        let lines = parse_gemini("### Sub-sub");
        assert_eq!(
            lines,
            vec![GeminiLine::Heading {
                level: 3,
                text: "Sub-sub".to_string(),
            }]
        );
    }

    #[test]
    fn test_list_item() {
        let lines = parse_gemini("* Item");
        assert_eq!(lines, vec![GeminiLine::ListItem("Item".to_string())]);
    }

    #[test]
    fn test_quote_line() {
        let lines = parse_gemini("> Quoted");
        assert_eq!(lines, vec![GeminiLine::Quote("Quoted".to_string())]);
    }

    #[test]
    fn test_preformatted_block() {
        let input = "```\nsome code\n```";
        let lines = parse_gemini(input);
        assert_eq!(
            lines,
            vec![
                GeminiLine::PreformattedToggle {
                    alt_text: String::new()
                },
                GeminiLine::PreformattedText("some code".to_string()),
                GeminiLine::PreformattedToggle {
                    alt_text: String::new()
                },
            ]
        );
    }

    #[test]
    fn test_preformatted_no_parsing() {
        let input = "```\n=> gemini://x/y Link\n```";
        let lines = parse_gemini(input);
        assert_eq!(
            lines,
            vec![
                GeminiLine::PreformattedToggle {
                    alt_text: String::new()
                },
                GeminiLine::PreformattedText("=> gemini://x/y Link".to_string()),
                GeminiLine::PreformattedToggle {
                    alt_text: String::new()
                },
            ]
        );
    }

    #[test]
    fn test_preformatted_alt_text() {
        let lines = parse_gemini("```alt");
        assert_eq!(
            lines,
            vec![GeminiLine::PreformattedToggle {
                alt_text: "alt".to_string()
            }]
        );
    }

    #[test]
    fn test_preformatted_unclosed() {
        let input = "```\nline1\nline2";
        let lines = parse_gemini(input);
        assert_eq!(
            lines,
            vec![
                GeminiLine::PreformattedToggle {
                    alt_text: String::new()
                },
                GeminiLine::PreformattedText("line1".to_string()),
                GeminiLine::PreformattedText("line2".to_string()),
            ]
        );
    }

    #[test]
    fn test_empty_document() {
        let lines = parse_gemini("");
        assert!(lines.is_empty());
    }

    #[test]
    fn test_mixed_content() {
        let input = "# Welcome\n\nSome text here.\n=> gemini://example.com/ Home\n* Item one\n* Item two\n> A quote\n```code\nfn main() {}\n```";
        let lines = parse_gemini(input);
        assert_eq!(
            lines,
            vec![
                GeminiLine::Heading {
                    level: 1,
                    text: "Welcome".to_string()
                },
                GeminiLine::Text(String::new()),
                GeminiLine::Text("Some text here.".to_string()),
                GeminiLine::Link {
                    url: "gemini://example.com/".to_string(),
                    label: "Home".to_string()
                },
                GeminiLine::ListItem("Item one".to_string()),
                GeminiLine::ListItem("Item two".to_string()),
                GeminiLine::Quote("A quote".to_string()),
                GeminiLine::PreformattedToggle {
                    alt_text: "code".to_string()
                },
                GeminiLine::PreformattedText("fn main() {}".to_string()),
                GeminiLine::PreformattedToggle {
                    alt_text: String::new()
                },
            ]
        );
    }
}
