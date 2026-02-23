use crate::parser::GeminiLine;

const RESET: &str = "\x1b[0m";
const BOLD_BRIGHT_CYAN: &str = "\x1b[1;96m";
const BOLD: &str = "\x1b[1m";
const CYAN: &str = "\x1b[36m";
const DIM_ITALIC: &str = "\x1b[2;3m";

/// Render parsed gemini lines into ANSI-formatted strings.
/// Returns a tuple of (output_lines, link_urls):
/// - output_lines: Vec<String> of formatted lines ready for display (one per output line)
/// - link_urls: Vec<String> of link URLs found on the page (indexed from 1)
///
/// Does NOT print to stdout — the caller (via the pager) handles display.
pub fn render(lines: &[GeminiLine]) -> (Vec<String>, Vec<String>) {
    let mut output_lines: Vec<String> = Vec::new();
    let mut links: Vec<String> = Vec::new();
    let mut in_preformatted = false;

    for line in lines {
        match line {
            GeminiLine::Text(text) => {
                output_lines.push(text.clone());
            }
            GeminiLine::Link { url, label } => {
                links.push(url.clone());
                let index = links.len();
                output_lines.push(format!("{CYAN}[{index}] {label}{RESET}"));
            }
            GeminiLine::Heading { level, text } => {
                if *level == 1 {
                    output_lines.push(format!("{BOLD_BRIGHT_CYAN}{text}{RESET}"));
                } else {
                    output_lines.push(format!("{BOLD}{text}{RESET}"));
                }
            }
            GeminiLine::ListItem(text) => {
                output_lines.push(format!("  \u{2022} {text}"));
            }
            GeminiLine::Quote(text) => {
                output_lines.push(format!("{DIM_ITALIC}{text}{RESET}"));
            }
            GeminiLine::PreformattedToggle { .. } => {
                in_preformatted = !in_preformatted;
                // Toggle lines produce no output
            }
            GeminiLine::PreformattedText(text) => {
                output_lines.push(text.clone());
            }
        }
    }

    let _ = in_preformatted;

    (output_lines, links)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_render_returns_links() {
        let lines = vec![
            GeminiLine::Link {
                url: "gemini://a.com/".to_string(),
                label: "Link A".to_string(),
            },
            GeminiLine::Text("Some text".to_string()),
            GeminiLine::Link {
                url: "gemini://b.com/".to_string(),
                label: "Link B".to_string(),
            },
            GeminiLine::Link {
                url: "gemini://c.com/page".to_string(),
                label: "Link C".to_string(),
            },
        ];

        let (output_lines, links) = render(&lines);
        assert_eq!(links.len(), 3);
        assert_eq!(links[0], "gemini://a.com/");
        assert_eq!(links[1], "gemini://b.com/");
        assert_eq!(links[2], "gemini://c.com/page");
        assert_eq!(output_lines.len(), 4);
    }

    #[test]
    fn test_render_link_numbering() {
        let lines = vec![
            GeminiLine::Link {
                url: "gemini://first.com/".to_string(),
                label: "First".to_string(),
            },
            GeminiLine::Link {
                url: "gemini://second.com/".to_string(),
                label: "Second".to_string(),
            },
        ];

        let (output_lines, links) = render(&lines);
        assert_eq!(links.len(), 2);
        assert_eq!(links[0], "gemini://first.com/");
        assert_eq!(links[1], "gemini://second.com/");
        // Check numbering in output
        assert!(output_lines[0].contains("[1]"));
        assert!(output_lines[1].contains("[2]"));
    }

    #[test]
    fn test_render_empty_page() {
        let lines: Vec<GeminiLine> = vec![];
        let (output_lines, links) = render(&lines);
        assert!(links.is_empty());
        assert!(output_lines.is_empty());
    }

    #[test]
    fn test_render_output_line_count() {
        let lines = vec![
            GeminiLine::Text("text".to_string()),
            GeminiLine::Heading {
                level: 1,
                text: "Title".to_string(),
            },
            GeminiLine::PreformattedToggle {
                alt_text: String::new(),
            },
            GeminiLine::PreformattedText("code".to_string()),
            GeminiLine::PreformattedToggle {
                alt_text: String::new(),
            },
        ];

        let (output_lines, _) = render(&lines);
        // 3 output lines: text, heading, preformatted text (toggles produce no output)
        assert_eq!(output_lines.len(), 3);
    }

    #[test]
    fn test_render_heading_format() {
        let lines = vec![GeminiLine::Heading {
            level: 1,
            text: "My Title".to_string(),
        }];

        let (output_lines, _) = render(&lines);
        assert_eq!(output_lines.len(), 1);
        assert!(output_lines[0].contains("My Title"));
        assert!(output_lines[0].contains(BOLD_BRIGHT_CYAN));
    }

    #[test]
    fn test_render_preformatted_toggle_hidden() {
        let lines = vec![
            GeminiLine::PreformattedToggle {
                alt_text: "rust".to_string(),
            },
            GeminiLine::PreformattedText("fn main() {}".to_string()),
            GeminiLine::PreformattedToggle {
                alt_text: String::new(),
            },
        ];

        let (output_lines, _) = render(&lines);
        // Only the preformatted text line should appear
        assert_eq!(output_lines.len(), 1);
        assert_eq!(output_lines[0], "fn main() {}");
    }
}
