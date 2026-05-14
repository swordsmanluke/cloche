package projectcli

import (
	"fmt"
	"io"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

// WriteReposList renders the "cloche project repos list" table to w.
func WriteReposList(repos []*pb.Repository, w io.Writer) {
	if len(repos) == 0 {
		fmt.Fprintln(w, "No repositories configured.")
		return
	}
	fmt.Fprintf(w, "%-20s  %-30s  %s\n", "NAME", "PATH", "URL")
	for _, repo := range repos {
		if repo.Url != "" {
			fmt.Fprintf(w, "%-20s  %-30s  %s\n", repo.Name, repo.Path, repo.Url)
		} else {
			fmt.Fprintf(w, "%-20s  %s\n", repo.Name, repo.Path)
		}
	}
}
