package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/adapters/web"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/help"
	"github.com/swordsmanluke/cloche/internal/mcpauth"
	"github.com/swordsmanluke/cloche/internal/ports"
)

func init() { registerScenarios(initAskUserParkScenarios) }

// Scenario times are scaled so the suite stays fast: one scenario second is
// askParkSecond of wall clock. The Claude MCP client's ~60s silence limit
// becomes askClientWindow; park_after defaults to five scenario minutes.
const (
	askParkSecond        = 5 * time.Millisecond
	askClientWindow      = 60 * askParkSecond
	askDefaultParkAfter  = 5 * 60 * askParkSecond
	askParkRunID         = "run-ask-park"
	askParkQuestion      = "Which schema should I use?"
	askParkExpectedReply = "Use schema B"
	askParkSecret        = "ask-park-secret"
)

func scaled(seconds int) time.Duration { return time.Duration(seconds) * askParkSecond }

// askParkCtx holds per-scenario state. It wires the real help.Router (over an
// in-memory sqlite store) to the real /mcp web handler; the only fake is the
// park callback, which stands in for the daemon tearing down the container.
type askParkCtx struct {
	parkAfter time.Duration
	started   bool

	store   *sqlite.Store
	router  *help.Router
	srv     *httptest.Server
	cancels []context.CancelFunc // aborts in-flight /mcp requests on teardown

	mu     sync.Mutex
	parked []string // run IDs the fake park callback was invoked for

	call *askParkCall
}

// askParkCall is one in-flight ask_user tools/call from an idle-timeout client.
type askParkCall struct {
	done chan struct{} // closed when the response stream ends

	mu         sync.Mutex
	finished   bool
	timedOut   bool // the client saw a full window of silence
	httpErr    error
	text       string
	isError    bool
	parkedFlag bool
	threadID   string
}

func (s *askParkCtx) reset() {
	s.teardown()
	*s = askParkCtx{parkAfter: askDefaultParkAfter}
}

func (s *askParkCtx) teardown() {
	for _, cancel := range s.cancels {
		cancel()
	}
	if s.srv != nil {
		s.srv.Close()
	}
	if s.store != nil {
		_ = s.store.Close() // in-memory store; nothing to flush
	}
}

func (s *askParkCtx) ensureStarted() error {
	if s.started {
		return nil
	}
	store, err := sqlite.NewStore(":memory:")
	if err != nil {
		return err
	}
	s.store = store
	s.router = help.NewRouter(store, s.parkAfter)
	s.router.SetParkFunc(func(_ context.Context, runID, _, _ string) {
		s.mu.Lock()
		s.parked = append(s.parked, runID)
		s.mu.Unlock()
	})
	secret := []byte(askParkSecret)
	askFn := func(ctx context.Context, runID, question, title, threadID string, options []string, askKey string) (string, string, bool, error) {
		res, err := s.router.Ask(ctx, help.AskParams{
			RunID: runID, StepName: "implement", Channel: "cloche",
			Question: question, Title: title, Options: options, ThreadID: threadID, AskKey: askKey,
		})
		return res.Answer, res.ThreadID, res.Parked, err
	}
	h, err := web.NewHandler(store, store, web.WithHelpMCP(secret, askFn), web.WithMCPKeepalive(askClientWindow/4))
	if err != nil {
		return err
	}
	s.srv = httptest.NewServer(h)
	s.started = true
	return nil
}

// startAsk issues the ask_user call over /mcp, as the agent's MCP client does.
func (s *askParkCtx) startAsk() error {
	if err := s.ensureStarted(); err != nil {
		return err
	}
	secret := []byte(askParkSecret)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ask_user", "arguments": map[string]any{"question": askParkQuestion}},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels = append(s.cancels, cancel)
	req, err := http.NewRequestWithContext(ctx, "POST", s.srv.URL+"/mcp?run_id="+askParkRunID, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+mcpauth.Token(secret, askParkRunID))
	req.Header.Set("Accept", "application/json, text/event-stream")

	c := &askParkCall{done: make(chan struct{})}
	s.call = c
	go c.run(req, cancel)
	return nil
}

// run reads the stream like an idle-timeout client: a full askClientWindow
// without any bytes counts as the client timing out.
func (c *askParkCall) run(req *http.Request, abort context.CancelFunc) {
	defer close(c.done)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.mu.Lock()
		c.httpErr = err
		c.mu.Unlock()
		return
	}
	defer resp.Body.Close()

	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	var body bytes.Buffer
	for {
		select {
		case b, ok := <-chunks:
			if !ok {
				c.parse(body.String())
				return
			}
			body.Write(b)
		case <-time.After(askClientWindow):
			c.mu.Lock()
			c.timedOut = true
			c.mu.Unlock()
			abort()
			return
		}
	}
}

func (c *askParkCall) parse(stream string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := strings.LastIndex(stream, "data: ")
	if idx < 0 {
		c.httpErr = fmt.Errorf("no final message in stream %q", stream)
		return
	}
	var msg struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError  bool   `json:"isError"`
			Parked   bool   `json:"parked"`
			ThreadID string `json:"threadId"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stream[idx+6:])), &msg); err != nil {
		c.httpErr = err
		return
	}
	c.finished = true
	c.isError = msg.Result.IsError
	c.parkedFlag = msg.Result.Parked
	c.threadID = msg.Result.ThreadID
	if len(msg.Result.Content) > 0 {
		c.text = msg.Result.Content[0].Text
	}
}

// waitDone waits for the call to end within d.
func (c *askParkCall) waitDone(d time.Duration) error {
	select {
	case <-c.done:
		return nil
	case <-time.After(d):
		return fmt.Errorf("ask_user call still blocked after %v", d)
	}
}

// waitThread blocks until the ask's help thread exists and returns it.
func (s *askParkCtx) waitThread() (*domain.HelpThread, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		threads, err := s.router.ListThreads(context.Background(), ports.HelpThreadFilter{})
		if err == nil && len(threads) > 0 {
			return threads[0], nil
		}
		time.Sleep(time.Millisecond)
	}
	return nil, fmt.Errorf("no help thread was created for the ask")
}

// reply is what "cloche threads reply" does: ReplyByAddress on <channel>/<name>.
func (s *askParkCtx) reply() error {
	th, err := s.waitThread()
	if err != nil {
		return err
	}
	return s.router.ReplyByAddress(context.Background(), th.Channel+"/"+th.Name, askParkExpectedReply, "cli")
}

func (s *askParkCtx) parkedRuns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.parked)
}

func initAskUserParkScenarios(ctx *godog.ScenarioContext) {
	s := &askParkCtx{parkAfter: askDefaultParkAfter}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.teardown()
		return c, nil
	})

	ctx.Step(`^an agent step whose agent calls ask_user over /mcp$`, func() error {
		return nil // the call itself is issued by the When step, after park_after is configured
	})
	ctx.Step(`^park_after is (\d+) minutes$`, func(m int) error {
		if s.started {
			return fmt.Errorf("park_after must be set before the ask starts")
		}
		s.parkAfter = scaled(m * 60)
		return nil
	})
	ctx.Step(`^a human replies after (\d+) seconds$`, func(sec int) error {
		if err := s.startAsk(); err != nil {
			return err
		}
		if _, err := s.waitThread(); err != nil {
			return err
		}
		time.Sleep(scaled(sec))
		if err := s.reply(); err != nil {
			return err
		}
		return s.call.waitDone(5 * time.Second)
	})
	ctx.Step(`^the agent receives the reply as the tool result$`, func() error {
		c := s.call
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.timedOut || c.httpErr != nil || !c.finished || c.isError || c.parkedFlag {
			return fmt.Errorf("expected a plain reply result, got timedOut=%v err=%v finished=%v isError=%v parked=%v",
				c.timedOut, c.httpErr, c.finished, c.isError, c.parkedFlag)
		}
		if c.text != askParkExpectedReply {
			return fmt.Errorf("tool result = %q, want %q", c.text, askParkExpectedReply)
		}
		return nil
	})
	ctx.Step(`^the run is not parked$`, func() error {
		if n := s.parkedRuns(); n != 0 {
			return fmt.Errorf("run was parked %d time(s)", n)
		}
		return nil
	})
	ctx.Step(`^nobody replies for (\d+) seconds$`, func(sec int) error {
		if err := s.startAsk(); err != nil {
			return err
		}
		time.Sleep(scaled(sec))
		return nil
	})
	ctx.Step(`^the agent never receives a timeout result$`, func() error {
		c := s.call
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.timedOut {
			return fmt.Errorf("client saw %v of silence: ask_user would time out", askClientWindow)
		}
		if c.finished || c.httpErr != nil {
			return fmt.Errorf("ask ended early (finished=%v err=%v text=%q)", c.finished, c.httpErr, c.text)
		}
		return nil
	})
	ctx.Step(`^the step does not fail$`, func() error {
		c := s.call
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.timedOut || c.isError || c.httpErr != nil {
			return fmt.Errorf("step failed: timedOut=%v isError=%v err=%v", c.timedOut, c.isError, c.httpErr)
		}
		return nil
	})
	ctx.Step(`^nobody replies for (\d+) minutes$`, func(m int) error {
		if err := s.startAsk(); err != nil {
			return err
		}
		// Wait for park_after to elapse; the call must end by itself.
		return s.call.waitDone(scaled(m*60) + 5*time.Second)
	})
	ctx.Step(`^the run is parked$`, func() error {
		if s.parkedRuns() != 1 {
			return fmt.Errorf("expected exactly 1 park for %s, got %d", askParkRunID, s.parkedRuns())
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.parked[0] != askParkRunID {
			return fmt.Errorf("parked run %q, want %q", s.parked[0], askParkRunID)
		}
		return nil
	})
	ctx.Step(`^the help thread is in state "([^"]*)"$`, func(state string) error {
		th, err := s.waitThread()
		if err != nil {
			return err
		}
		got, _, err := s.store.GetThread(context.Background(), th.ID)
		if err != nil {
			return err
		}
		if string(got.State) != state {
			return fmt.Errorf("thread state = %q, want %q", got.State, state)
		}
		return nil
	})
	ctx.Step(`^the ask has been parked$`, func() error {
		if err := s.startAsk(); err != nil {
			return err
		}
		if err := s.call.waitDone(s.parkAfter + 5*time.Second); err != nil {
			return err
		}
		s.call.mu.Lock()
		defer s.call.mu.Unlock()
		if !s.call.parkedFlag || s.parkedRuns() != 1 {
			return fmt.Errorf("ask did not park (parkedFlag=%v parks=%d)", s.call.parkedFlag, s.parkedRuns())
		}
		return nil
	})
	ctx.Step(`^a human runs "cloche threads reply" on the thread$`, func() error {
		return s.reply()
	})
	ctx.Step(`^the run resumes with the reply as the answer to the ask$`, func() error {
		// On resume the step re-runs and re-issues the same ask; the router
		// answers it from the recorded reply instead of blocking or re-parking.
		if err := s.startAsk(); err != nil {
			return err
		}
		if err := s.call.waitDone(5 * time.Second); err != nil {
			return err
		}
		c := s.call
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.text != askParkExpectedReply || c.parkedFlag || c.isError {
			return fmt.Errorf("resumed ask returned text=%q parked=%v isError=%v", c.text, c.parkedFlag, c.isError)
		}
		if s.parkedRuns() != 1 {
			return fmt.Errorf("resume parked again (parks=%d)", s.parkedRuns())
		}
		return nil
	})

	// gRPC path: "clo ask" -> ClocheServer.AskHelp -> help.Router.Ask, the same
	// router and park callback used above, so it is driven through the router
	// directly (no /mcp, no SSE) rather than standing up a gRPC server.
	type grpcAsk struct {
		res help.AskResult
		err error
	}
	var grpcDone chan grpcAsk
	ctx.Step(`^an agent step whose agent calls "clo ask" over gRPC$`, func() error {
		if err := s.ensureStarted(); err != nil {
			return err
		}
		grpcDone = make(chan grpcAsk, 1)
		go func() {
			res, err := s.router.Ask(context.Background(), help.AskParams{
				RunID: askParkRunID, StepName: "implement", Channel: "cloche", Question: askParkQuestion,
			})
			grpcDone <- grpcAsk{res, err}
		}()
		return nil
	})
	ctx.Step(`^nobody replies until park_after elapses$`, func() error {
		select {
		case a := <-grpcDone:
			if a.err != nil {
				return a.err
			}
			if !a.res.Parked {
				return fmt.Errorf("ask returned without parking: %+v", a.res)
			}
			return nil
		case <-time.After(s.parkAfter + 5*time.Second):
			return fmt.Errorf("gRPC-path ask did not park after park_after")
		}
	})
	ctx.Step(`^a reply via "cloche threads reply" resumes the run$`, func() error {
		if err := s.reply(); err != nil {
			return err
		}
		res, err := s.router.Ask(context.Background(), help.AskParams{
			RunID: askParkRunID, StepName: "implement", Channel: "cloche", Question: askParkQuestion,
		})
		if err != nil {
			return err
		}
		if res.Answer != askParkExpectedReply || res.Parked {
			return fmt.Errorf("resumed ask = %+v, want answer %q", res, askParkExpectedReply)
		}
		return nil
	})
}
