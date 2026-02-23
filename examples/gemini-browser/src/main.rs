mod gemini;
mod pager;
mod parser;
mod render;
mod url_utils;

use std::io::{self, BufRead, Write};

use url::Url;

use gemini::GeminiResponse;

/// Holds the browser's runtime state.
struct BrowserState {
    history: Vec<Url>,
    current_url: Option<Url>,
    links: Vec<String>,
    pager: Option<pager::Pager>,
}

/// Commands parsed from the REPL prompt.
enum Command {
    FollowLink(usize),
    Back,
    Go(String),
    Quit,
    Help,
    Navigate(String),
    NextPage,
    PrevPage,
    Empty,
    Unknown,
}

fn parse_command(input: &str) -> Command {
    let trimmed = input.trim();

    if trimmed.is_empty() {
        return Command::Empty;
    }

    match trimmed {
        "quit" | "q" => Command::Quit,
        "back" | "b" => Command::Back,
        "help" | "?" => Command::Help,
        "next" | "n" => Command::NextPage,
        "prev" | "p" => Command::PrevPage,
        _ => {
            if let Some(url) = trimmed.strip_prefix("go ") {
                let url = url.trim();
                if url.is_empty() {
                    Command::Unknown
                } else {
                    Command::Go(url.to_string())
                }
            } else if trimmed.starts_with("gemini://") {
                Command::Navigate(trimmed.to_string())
            } else if let Ok(n) = trimmed.parse::<usize>() {
                Command::FollowLink(n)
            } else {
                Command::Unknown
            }
        }
    }
}

fn navigate(state: &mut BrowserState, url: Url) {
    match gemini::fetch_with_redirects(&url) {
        Ok((response, final_url)) => {
            handle_response(state, response, final_url);
        }
        Err(e) => {
            eprintln!("Error: {e}");
        }
    }
}

fn handle_response(state: &mut BrowserState, response: GeminiResponse, url: Url) {
    let first_digit = response.status / 10;

    match first_digit {
        1 => {
            // INPUT: prompt the user
            println!("{}", response.meta);
            print!("Input: ");
            let _ = io::stdout().flush();

            let stdin = io::stdin();
            let mut input = String::new();
            if stdin.lock().read_line(&mut input).is_ok() {
                let input = input.trim_end_matches('\n').trim_end_matches('\r');
                let mut new_url = url;
                let encoded: String = url::form_urlencoded::Serializer::new(String::new())
                    .append_key_only(input)
                    .finish();
                new_url.set_query(Some(&encoded));
                navigate(state, new_url);
            }
        }
        2 => {
            // SUCCESS
            let mime = if response.meta.is_empty() {
                "text/gemini"
            } else {
                &response.meta
            };

            if mime.starts_with("text/gemini") {
                let body_bytes = response.body.unwrap_or_default();
                let body_str = String::from_utf8_lossy(&body_bytes);
                let parsed = parser::parse_gemini(&body_str);
                let (output_lines, links) = render::render(&parsed);

                // Determine pagination
                let height = pager::terminal_height();
                let page_height = std::cmp::max(height.saturating_sub(1), 1);

                if output_lines.len() > page_height {
                    let pg = pager::Pager::new(output_lines, page_height);
                    pg.display_current_page();
                    state.pager = Some(pg);
                } else {
                    for line in &output_lines {
                        println!("{line}");
                    }
                    state.pager = None;
                }

                // Update state
                if let Some(old_url) = state.current_url.take() {
                    state.history.push(old_url);
                }
                state.current_url = Some(url);
                state.links = links;
            } else {
                println!("[Received {mime}, not rendering]");
                // Still update navigation state
                if let Some(old_url) = state.current_url.take() {
                    state.history.push(old_url);
                }
                state.current_url = Some(url);
                state.links.clear();
                state.pager = None;
            }
        }
        4 => {
            eprintln!("Temporary failure ({}): {}", response.status, response.meta);
        }
        5 => {
            eprintln!("Permanent failure ({}): {}", response.status, response.meta);
        }
        6 => {
            eprintln!("Client certificates are not supported.");
        }
        _ => {
            eprintln!("Unexpected status: {} {}", response.status, response.meta);
        }
    }
}

fn print_help() {
    println!("Commands:");
    println!("  <number>       Follow link by number");
    println!("  back, b        Go to previous page");
    println!("  go <url>       Navigate to a URL");
    println!("  gemini://...   Navigate to a Gemini URL");
    println!("  next, n        Next page (when content is paginated)");
    println!("  prev, p        Previous page (when content is paginated)");
    println!("  help, ?        Show this help");
    println!("  quit, q        Exit the browser");
}

fn main() {
    // Install the default crypto provider for rustls
    let _ = rustls::crypto::ring::default_provider().install_default();

    let mut state = BrowserState {
        history: Vec::new(),
        current_url: None,
        links: Vec::new(),
        pager: None,
    };

    // Check if a URL was provided as a command line argument
    let args: Vec<String> = std::env::args().collect();
    if args.len() > 1 {
        match url_utils::parse_gemini_url(&args[1]) {
            Ok(url) => navigate(&mut state, url),
            Err(e) => eprintln!("Error: {e}"),
        }
    }

    let stdin = io::stdin();

    loop {
        print!("> ");
        let _ = io::stdout().flush();

        let mut line = String::new();
        match stdin.lock().read_line(&mut line) {
            Ok(0) => break, // EOF
            Ok(_) => {}
            Err(e) => {
                eprintln!("Error reading input: {e}");
                break;
            }
        }

        match parse_command(&line) {
            Command::Empty => continue,
            Command::Quit => break,
            Command::Help => print_help(),
            Command::Back => {
                if let Some(prev_url) = state.history.pop() {
                    let url = prev_url.clone();
                    // Don't push current to history when going back
                    state.current_url = None;
                    navigate(&mut state, url);
                } else {
                    println!("No previous page.");
                }
            }
            Command::Go(raw_url) => match url_utils::parse_gemini_url(&raw_url) {
                Ok(url) => navigate(&mut state, url),
                Err(e) => eprintln!("Error: {e}"),
            },
            Command::Navigate(raw_url) => match url_utils::parse_gemini_url(&raw_url) {
                Ok(url) => navigate(&mut state, url),
                Err(e) => eprintln!("Error: {e}"),
            },
            Command::FollowLink(n) => {
                if n < 1 || n > state.links.len() {
                    println!("Invalid link number.");
                } else {
                    let link_url = state.links[n - 1].clone();
                    match state.current_url.as_ref() {
                        Some(base) => match url_utils::resolve_url(base, &link_url) {
                            Ok(url) => navigate(&mut state, url),
                            Err(e) => eprintln!("Error: {e}"),
                        },
                        None => {
                            // No current URL to resolve against, try parsing as absolute
                            match url_utils::parse_gemini_url(&link_url) {
                                Ok(url) => navigate(&mut state, url),
                                Err(e) => eprintln!("Error: {e}"),
                            }
                        }
                    }
                }
            }
            Command::NextPage => {
                if let Some(ref mut pg) = state.pager {
                    if pg.next_page() {
                        pg.display_current_page();
                    } else {
                        println!("Already on the last page.");
                    }
                } else {
                    println!("No page to scroll.");
                }
            }
            Command::PrevPage => {
                if let Some(ref mut pg) = state.pager {
                    if pg.prev_page() {
                        pg.display_current_page();
                    } else {
                        println!("Already on the first page.");
                    }
                } else {
                    println!("No page to scroll.");
                }
            }
            Command::Unknown => {
                println!("Unknown command. Type 'help' for available commands.");
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_quit() {
        assert!(matches!(parse_command("quit"), Command::Quit));
        assert!(matches!(parse_command("q"), Command::Quit));
    }

    #[test]
    fn test_parse_back() {
        assert!(matches!(parse_command("back"), Command::Back));
        assert!(matches!(parse_command("b"), Command::Back));
    }

    #[test]
    fn test_parse_help() {
        assert!(matches!(parse_command("help"), Command::Help));
        assert!(matches!(parse_command("?"), Command::Help));
    }

    #[test]
    fn test_parse_go() {
        match parse_command("go gemini://example.com") {
            Command::Go(url) => assert_eq!(url, "gemini://example.com"),
            _ => panic!("expected Go command"),
        }
    }

    #[test]
    fn test_parse_navigate() {
        match parse_command("gemini://example.com/path") {
            Command::Navigate(url) => assert_eq!(url, "gemini://example.com/path"),
            _ => panic!("expected Navigate command"),
        }
    }

    #[test]
    fn test_parse_follow_link() {
        match parse_command("3") {
            Command::FollowLink(n) => assert_eq!(n, 3),
            _ => panic!("expected FollowLink command"),
        }
    }

    #[test]
    fn test_parse_empty() {
        assert!(matches!(parse_command(""), Command::Empty));
        assert!(matches!(parse_command("  "), Command::Empty));
    }

    #[test]
    fn test_parse_unknown() {
        assert!(matches!(parse_command("foo"), Command::Unknown));
    }

    #[test]
    fn test_parse_next() {
        assert!(matches!(parse_command("next"), Command::NextPage));
        assert!(matches!(parse_command("n"), Command::NextPage));
    }

    #[test]
    fn test_parse_prev() {
        assert!(matches!(parse_command("prev"), Command::PrevPage));
        assert!(matches!(parse_command("p"), Command::PrevPage));
    }
}
