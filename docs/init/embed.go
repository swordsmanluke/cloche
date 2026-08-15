// Package initdocs embeds the getting-started tutorial series so that
// `cloche init --new` can bundle a copy into new projects under
// .cloche/docs/init/, keeping the docs available offline next to the
// scaffold they describe.
package initdocs

import "embed"

// FS holds the tutorial markdown files (README.md and the numbered series).
//
//go:embed *.md
var FS embed.FS
