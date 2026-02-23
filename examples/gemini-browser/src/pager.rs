/// Paginated display for rendered output lines.
///
/// When a page's content exceeds the terminal height, the Pager shows one
/// screenful at a time, allowing the user to scroll forward and backward
/// through the content using `next`/`prev` commands at the REPL prompt.
pub struct Pager {
    lines: Vec<String>,
    offset: usize,
    page_height: usize,
}

impl Pager {
    /// Create a new Pager for the given lines.
    /// `page_height` is the number of content lines per page (typically
    /// terminal_height - 1, reserving one line for the status/pager prompt).
    pub fn new(lines: Vec<String>, page_height: usize) -> Self {
        Self {
            lines,
            offset: 0,
            page_height: page_height.max(1),
        }
    }

    /// Print the current page of lines to stdout.
    /// After the content lines, if there is more content beyond the current
    /// page, prints a status line: `[Page X/Y — 'n':next 'p':prev]`
    pub fn display_current_page(&self) {
        let end = std::cmp::min(self.offset + self.page_height, self.lines.len());
        for line in &self.lines[self.offset..end] {
            println!("{line}");
        }
        if self.needs_pagination() {
            println!(
                "\x1b[2m[Page {}/{} \u{2014} 'n':next 'p':prev]\x1b[0m",
                self.current_page(),
                self.total_pages()
            );
        }
    }

    /// Advance to the next page. Returns true if the page changed,
    /// false if already at the last page.
    pub fn next_page(&mut self) -> bool {
        let last_page_offset = if self.lines.is_empty() {
            0
        } else {
            (self.total_pages() - 1) * self.page_height
        };

        if self.offset >= last_page_offset {
            return false;
        }
        self.offset = std::cmp::min(self.offset + self.page_height, last_page_offset);
        true
    }

    /// Go back to the previous page. Returns true if the page changed,
    /// false if already at the first page.
    pub fn prev_page(&mut self) -> bool {
        if self.offset == 0 {
            return false;
        }
        self.offset = self.offset.saturating_sub(self.page_height);
        true
    }

    /// Returns true if the content requires pagination (more lines than page_height).
    pub fn needs_pagination(&self) -> bool {
        self.lines.len() > self.page_height
    }

    /// Returns the total number of pages.
    pub fn total_pages(&self) -> usize {
        if self.lines.is_empty() {
            return 1;
        }
        self.lines.len().div_ceil(self.page_height)
    }

    /// Returns the current page number (1-indexed).
    pub fn current_page(&self) -> usize {
        (self.offset / self.page_height) + 1
    }
}

/// Detect the terminal height in rows.
///
/// Strategy:
/// 1. Try the LINES environment variable (set by many terminals/shells).
/// 2. Try reading from the terminal via libc ioctl TIOCGWINSZ on stdout.
/// 3. Fall back to a default of 24 rows.
pub fn terminal_height() -> usize {
    // 1. Check LINES env var
    if let Ok(val) = std::env::var("LINES") {
        if let Ok(n) = val.parse::<usize>() {
            if n > 0 {
                return n;
            }
        }
    }

    // 2. ioctl TIOCGWINSZ
    #[cfg(unix)]
    {
        use libc::{ioctl, winsize, STDOUT_FILENO, TIOCGWINSZ};
        let mut ws: winsize = unsafe { std::mem::zeroed() };
        if unsafe { ioctl(STDOUT_FILENO, TIOCGWINSZ, &mut ws) } == 0 && ws.ws_row > 0 {
            return ws.ws_row as usize;
        }
    }

    // 3. Default
    24
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_lines(n: usize) -> Vec<String> {
        (0..n).map(|i| format!("Line {i}")).collect()
    }

    #[test]
    fn test_needs_pagination_short() {
        let pager = Pager::new(make_lines(5), 10);
        assert!(!pager.needs_pagination());
    }

    #[test]
    fn test_needs_pagination_long() {
        let pager = Pager::new(make_lines(15), 10);
        assert!(pager.needs_pagination());
    }

    #[test]
    fn test_needs_pagination_exact() {
        let pager = Pager::new(make_lines(10), 10);
        assert!(!pager.needs_pagination());
    }

    #[test]
    fn test_total_pages() {
        let pager = Pager::new(make_lines(25), 10);
        assert_eq!(pager.total_pages(), 3);
    }

    #[test]
    fn test_total_pages_exact() {
        let pager = Pager::new(make_lines(20), 10);
        assert_eq!(pager.total_pages(), 2);
    }

    #[test]
    fn test_total_pages_single() {
        let pager = Pager::new(make_lines(5), 10);
        assert_eq!(pager.total_pages(), 1);
    }

    #[test]
    fn test_total_pages_empty() {
        let pager = Pager::new(vec![], 10);
        assert_eq!(pager.total_pages(), 1);
    }

    #[test]
    fn test_current_page_initial() {
        let pager = Pager::new(make_lines(25), 10);
        assert_eq!(pager.current_page(), 1);
    }

    #[test]
    fn test_next_page_advances() {
        let mut pager = Pager::new(make_lines(25), 10);
        assert!(pager.next_page());
        assert_eq!(pager.current_page(), 2);
    }

    #[test]
    fn test_next_page_at_end() {
        let mut pager = Pager::new(make_lines(25), 10);
        pager.next_page();
        pager.next_page();
        // Now on page 3 (last page)
        assert!(!pager.next_page());
        assert_eq!(pager.current_page(), 3);
    }

    #[test]
    fn test_prev_page_at_start() {
        let mut pager = Pager::new(make_lines(25), 10);
        assert!(!pager.prev_page());
        assert_eq!(pager.current_page(), 1);
    }

    #[test]
    fn test_prev_page_goes_back() {
        let mut pager = Pager::new(make_lines(25), 10);
        pager.next_page();
        assert_eq!(pager.current_page(), 2);
        assert!(pager.prev_page());
        assert_eq!(pager.current_page(), 1);
    }

    #[test]
    fn test_page_height_clamp() {
        let pager = Pager::new(make_lines(5), 0);
        assert_eq!(pager.page_height, 1);
    }
}
