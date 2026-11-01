// Package spill reads an agent's output line by line with no line too long to
// read. A line over the limit is written to a file in the run's output
// directory and handed on as a short JSON stub carrying its path, so a huge
// tool result (an image Read, a large file dump) neither stops the reader nor
// fills the logs. Large data is passed by path, never inline.
package spill

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// DefaultLimit is the longest line handed on as is.
const DefaultLimit = 1024 * 1024

// Stub is the line that replaces a spilled one.
type Stub struct {
	Type  string `json:"type"` // always "cloche_spilled"
	Bytes int64  `json:"bytes"`
	Path  string `json:"path"`
}

// Lines reads r to EOF and calls fn with each line (without its newline). A
// line longer than limit is written to dir/<prefix>-<n>.out and fn gets a Stub
// as JSON instead. It always drains r, so the writer never blocks on a full
// pipe; a spill file that can't be written is dropped (the stub says so with
// an empty path) and reading goes on.
func Lines(r io.Reader, limit int, dir, prefix string, fn func(line []byte)) error {
	br := bufio.NewReaderSize(r, 256*1024)
	n := 0
	for {
		line, err := readLine(br, limit, func() (io.WriteCloser, string) {
			n++
			if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
				return nil, ""
			}
			path := filepath.Join(dir, fmt.Sprintf("%s-%d.out", prefix, n))
			f, ferr := os.Create(path)
			if ferr != nil {
				return nil, ""
			}
			return f, path
		})
		if line != nil {
			fn(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLine returns the next line, or a stub when it is longer than limit (the
// line then goes to the writer open returns). A final line without a newline
// is returned with io.EOF.
func readLine(br *bufio.Reader, limit int, open func() (io.WriteCloser, string)) ([]byte, error) {
	var buf []byte
	var w io.WriteCloser
	var path string
	var size int64
	for {
		chunk, err := br.ReadSlice('\n')
		done := err == nil
		if done {
			chunk = chunk[:len(chunk)-1]
		}
		if w == nil && path == "" && len(buf)+len(chunk) > limit {
			w, path = open()
			if w != nil {
				_, _ = w.Write(buf)
			}
			size = int64(len(buf))
			buf = nil
			if w == nil {
				path = "-" // spilling failed: drop the line, keep reading
			}
		}
		if path != "" {
			if w != nil {
				_, _ = w.Write(chunk)
			}
			size += int64(len(chunk))
		} else {
			buf = append(buf, chunk...)
		}
		if done || (err != nil && !errors.Is(err, bufio.ErrBufferFull)) {
			if path != "" {
				if w != nil {
					_ = w.Close()
				}
				p := path
				if p == "-" {
					p = ""
				}
				stub, _ := json.Marshal(Stub{Type: "cloche_spilled", Bytes: size, Path: p})
				if done {
					return stub, nil
				}
				return stub, err
			}
			if done {
				return buf, nil
			}
			if len(buf) == 0 {
				return nil, err
			}
			return buf, err
		}
	}
}
