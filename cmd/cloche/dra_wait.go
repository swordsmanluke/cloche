package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/swordsmanluke/cloche/internal/dra"
)

const draWaitUsage = "usage: cloche dra wait <poll> [--arg k=v ...] [--interval <dur>] [--timeout <dur>] --note <text>\n" +
	"       cloche dra wait questions --ids Q14,Q15 [--interval <dur>] [--timeout <dur>] --note <text>\n" +
	"       cloche dra wait --cancel <id>\n" +
	"       cloche dra waits"

const (
	waitQuestionsPoll = dra.QuestionsPoll
)

// waitRequest is what "cloche dra wait" asks the daemon to register. The DRA
// only ever names a poll; it never supplies script text.
type waitRequest = dra.WaitRequest

// waitInfo is one pending wait as "cloche dra waits" shows it.
type waitInfo = dra.Wait

// waitClient is the daemon's wait API as the CLI sees it. The daemon knows
// which DRA is asking from its credential.
type waitClient interface {
	Register(req waitRequest) (id string, err error)
	List() ([]waitInfo, error)
	Cancel(id string) error
}

// newWaitClient returns the client for the DRA credential in the environment.
// A variable so tests can substitute a client.
var newWaitClient = func() (waitClient, error) {
	c := draModeClient()
	if c == nil {
		return nil, fmt.Errorf("%s and %s are not set: run this from a DRA wake-up", "CLOCHE_DRA_URL", "CLOCHE_DRA_TOKEN")
	}
	return c, nil
}

func (c *draClient) Register(req waitRequest) (string, error) {
	data, err := c.do("POST", "/api/dra/waits", req)
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &r); err != nil || r.ID == "" {
		return "", fmt.Errorf("unexpected response from the daemon: %s", strings.TrimSpace(string(data)))
	}
	return r.ID, nil
}

func (c *draClient) List() ([]waitInfo, error) {
	data, err := c.do("GET", "/api/dra/waits", nil)
	if err != nil {
		return nil, err
	}
	var waits []waitInfo
	if err := json.Unmarshal(data, &waits); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return waits, nil
}

func (c *draClient) Cancel(id string) error {
	_, err := c.do("POST", "/api/dra/waits/cancel", map[string]string{"id": id})
	return err
}

// draWaitCommand runs "cloche dra wait ...".
func draWaitCommand(rest []string, w io.Writer) error {
	var cancel, ids string
	req := waitRequest{Args: map[string]string{}, }
	haveNote := false
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch a {
		case "--cancel", "--ids", "--arg", "--interval", "--timeout", "--note":
			if i+1 >= len(rest) {
				return fmt.Errorf("%s needs a value\n%s", a, draWaitUsage)
			}
			i++
			v := rest[i]
			switch a {
			case "--cancel":
				cancel = v
			case "--ids":
				ids = v
			case "--note":
				req.Note, haveNote = v, true
			case "--arg":
				k, val, ok := strings.Cut(v, "=")
				if !ok || k == "" {
					return fmt.Errorf("--arg wants key=value, got %q", v)
				}
				req.Args[k] = val
			default:
				d, err := time.ParseDuration(v)
				if err != nil || d <= 0 {
					return fmt.Errorf("%s: invalid duration %q", a, v)
				}
				if a == "--interval" {
					req.Interval = d
				} else {
					req.Timeout = d
				}
			}
		default:
			if req.Poll == "" && !strings.HasPrefix(a, "-") {
				req.Poll = a
				continue
			}
			return fmt.Errorf("unknown argument %q\n%s", a, draWaitUsage)
		}
	}
	client, err := newWaitClient()
	if err != nil {
		return err
	}
	if cancel != "" {
		if req.Poll != "" || ids != "" || haveNote {
			return fmt.Errorf("--cancel takes only a wait id\n%s", draWaitUsage)
		}
		if err := client.Cancel(cancel); err != nil {
			return err
		}
		fmt.Fprintf(w, "Cancelled wait %s\n", cancel)
		return nil
	}
	if req.Poll == "" || !haveNote || strings.TrimSpace(req.Note) == "" {
		return fmt.Errorf("%s", draWaitUsage)
	}
	if req.Poll == waitQuestionsPoll {
		var list []string
		for _, id := range strings.Split(ids, ",") {
			if id = strings.TrimSpace(id); id != "" {
				list = append(list, id)
			}
		}
		if len(list) == 0 || len(req.Args) > 0 {
			return fmt.Errorf("the questions poll takes --ids Q14,Q15 and no --arg\n%s", draWaitUsage)
		}
		req.Args = map[string]string{"ids": strings.Join(list, ",")}
	} else if ids != "" {
		return fmt.Errorf("--ids only applies to the questions poll")
	}
	if len(req.Args) == 0 {
		req.Args = nil
	}
	id, err := client.Register(req)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Registered wait %s\n", id)
	return nil
}

// draWaitsCommand runs "cloche dra waits".
func draWaitsCommand(rest []string, w io.Writer) error {
	if len(rest) > 0 {
		return fmt.Errorf("unknown argument %q\n%s", rest[0], draWaitUsage)
	}
	client, err := newWaitClient()
	if err != nil {
		return err
	}
	waits, err := client.List()
	if err != nil {
		return err
	}
	if len(waits) == 0 {
		fmt.Fprintln(w, "No pending waits.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPOLL\tARGS\tINTERVAL\tTIMEOUT\tNOTE")
	for _, x := range waits {
		poll := x.Poll
		if poll == waitQuestionsPoll {
			poll = "questions (" + strings.ReplaceAll(x.Args["ids"], ",", ", ") + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", x.ID, poll, formatWaitArgs(x), x.Interval, x.Timeout, x.Note)
	}
	return tw.Flush()
}

func formatWaitArgs(x waitInfo) string {
	if x.Poll == waitQuestionsPoll || len(x.Args) == 0 {
		return "-"
	}
	var kv []string
	for k, v := range x.Args {
		kv = append(kv, k+"="+v)
	}
	sort.Strings(kv)
	return strings.Join(kv, " ")
}
