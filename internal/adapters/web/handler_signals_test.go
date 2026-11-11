package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/signal"
)

type signalEnv struct {
	h      *Handler
	store  *sqlite.Store
	tokens *signal.TokenStore
	disp   *signal.Dispatcher
	dir    string
	slug   string
	token  string

	mu      sync.Mutex
	handled []signal.Signal
	gate    chan struct{} // handlers block until closed
	started chan struct{} // one send per started handler
}

func setupSignals(t *testing.T) *signalEnv {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	e := &signalEnv{store: store, gate: make(chan struct{}), started: make(chan struct{}, 100)}
	e.tokens = signal.NewTokenStore(t.TempDir())
	e.disp = &signal.Dispatcher{Store: store, Handler: func(_ context.Context, _ string, sig signal.Signal, _ *signal.InFlight) error {
		e.mu.Lock()
		e.handled = append(e.handled, sig)
		e.mu.Unlock()
		e.started <- struct{}{}
		<-e.gate
		return nil
	}}
	h, err := NewHandler(store, store, WithSignalIntake(e.tokens, e.disp))
	require.NoError(t, err)
	e.h = h

	e.dir = t.TempDir()
	e.slug = filepath.Base(e.dir)
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, e.dir)
	e.token, err = e.tokens.Get(e.dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		select {
		case <-e.gate:
		default:
			close(e.gate)
		}
		e.disp.Wait()
	})
	return e
}

func (e *signalEnv) post(t *testing.T, project, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/projects/"+project+"/signals", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

const validSignal = `{"source":"ci-main","key":"main-red","severity":"urgent","title":"main is red","evidence":"FAIL x","domain":"ci"}`

func TestSignals_AcceptedStoredAndHandled(t *testing.T) {
	e := setupSignals(t)
	w := e.post(t, e.slug, "Bearer "+e.token, validSignal)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

	var resp struct {
		ID        int64 `json:"id"`
		Coalesced bool  `json:"coalesced"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotZero(t, resp.ID)
	assert.False(t, resp.Coalesced)

	recs, err := e.store.ListSignals(context.Background(), e.dir)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, signal.Signal{Source: "ci-main", Key: "main-red", Severity: "urgent",
		Title: "main is red", Evidence: "FAIL x", Domain: "ci"}, recs[0].Signal)

	<-e.started
	e.mu.Lock()
	defer e.mu.Unlock()
	require.Len(t, e.handled, 1)
	assert.Equal(t, "main-red", e.handled[0].Key)
}

func TestSignals_Auth(t *testing.T) {
	e := setupSignals(t)
	other := t.TempDir()
	otherTok, err := e.tokens.Get(other)
	require.NoError(t, err)

	cases := []struct{ name, project, auth string }{
		{"no header", e.slug, ""},
		{"wrong token", e.slug, "Bearer nope"},
		{"empty bearer", e.slug, "Bearer "},
		{"basic scheme", e.slug, "Basic " + e.token},
		{"another project's token", e.slug, "Bearer " + otherTok},
		{"unknown project", "no-such-project", "Bearer " + e.token},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := e.post(t, tc.project, tc.auth, validSignal)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.NotEmpty(t, w.Header().Get("WWW-Authenticate"))
		})
	}
	recs, _ := e.store.ListSignals(context.Background(), e.dir)
	assert.Empty(t, recs, "rejected requests must not store anything")
	assert.Empty(t, e.started)
}

func TestSignals_RotatedTokenTakesEffectImmediately(t *testing.T) {
	e := setupSignals(t)
	newTok, err := e.tokens.Rotate(e.dir)
	require.NoError(t, err)
	require.NotEqual(t, e.token, newTok)

	assert.Equal(t, http.StatusUnauthorized, e.post(t, e.slug, "Bearer "+e.token, validSignal).Code)
	assert.Equal(t, http.StatusAccepted, e.post(t, e.slug, "Bearer "+newTok, validSignal).Code)
}

func TestSignals_NoTokenYetRejectsEverything(t *testing.T) {
	e := setupSignals(t)
	dir2 := t.TempDir()
	seedRunWithProject(t, e.store, "run-2", "develop", domain.RunStateSucceeded, dir2)
	// dir2 never had a token generated; Valid must not create one.
	w := e.post(t, filepath.Base(dir2), "Bearer anything", validSignal)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSignals_Validation(t *testing.T) {
	e := setupSignals(t)
	long := strings.Repeat("a", signal.MaxTitleLen+1)
	cases := []struct{ name, body, wantErr string }{
		{"not json", `nope`, "invalid JSON"},
		{"empty object", `{}`, "source is required"},
		{"missing key", `{"source":"s","severity":"low","title":"t"}`, "key is required"},
		{"missing severity", `{"source":"s","key":"k","title":"t"}`, "severity is required"},
		{"missing title", `{"source":"s","key":"k","severity":"low"}`, "title is required"},
		{"unknown severity", `{"source":"s","key":"k","severity":"critical","title":"t"}`, "severity must be one of"},
		{"severity case", `{"source":"s","key":"k","severity":"HIGH","title":"t"}`, "severity must be one of"},
		{"blank key", `{"source":"s","key":"  ","severity":"low","title":"t"}`, "key is required"},
		{"bad source chars", `{"source":"a b","key":"k","severity":"low","title":"t"}`, "source must be"},
		{"title too long", `{"source":"s","key":"k","severity":"low","title":"` + long + `"}`, "title must be at most"},
		{"multiline title", `{"source":"s","key":"k","severity":"low","title":"a\nb"}`, "single line"},
		{"bad domain", `{"source":"s","key":"k","severity":"low","title":"t","domain":"a/b"}`, "domain must be"},
		{"unknown field", `{"source":"s","key":"k","severity":"low","title":"t","extra":1}`, "unknown field"},
		{"wrong type", `{"source":1,"key":"k","severity":"low","title":"t"}`, "invalid JSON"},
		{"trailing data", validSignal + `{}`, "unexpected data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := e.post(t, e.slug, "Bearer "+e.token, tc.body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tc.wantErr)
		})
	}
	recs, _ := e.store.ListSignals(context.Background(), e.dir)
	assert.Empty(t, recs)
}

func TestSignals_BodyTooLarge(t *testing.T) {
	e := setupSignals(t)
	body := `{"source":"s","key":"k","severity":"low","title":"t","evidence":"` + strings.Repeat("x", signal.MaxBodyBytes) + `"}`
	w := e.post(t, e.slug, "Bearer "+e.token, body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestSignals_OptionalFieldsOmitted(t *testing.T) {
	e := setupSignals(t)
	w := e.post(t, e.slug, "Bearer "+e.token, `{"source":"s","key":"k","severity":"low","title":"t"}`)
	assert.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
}

func TestSignals_BurstCoalescesIntoOneHandler(t *testing.T) {
	e := setupSignals(t)
	auth := "Bearer " + e.token

	first := e.post(t, e.slug, auth, validSignal)
	require.Equal(t, http.StatusAccepted, first.Code)
	<-e.started // handler is now running (blocked on the gate)

	const burst = 50
	for i := 0; i < burst; i++ {
		w := e.post(t, e.slug, auth, validSignal)
		require.Equal(t, http.StatusAccepted, w.Code)
		assert.Contains(t, w.Body.String(), `"coalesced":true`)
	}
	// A different key is a different problem: its own handler.
	other := e.post(t, e.slug, auth, strings.Replace(validSignal, "main-red", "other-key", 1))
	require.Equal(t, http.StatusAccepted, other.Code)
	assert.Contains(t, other.Body.String(), `"coalesced":false`)
	<-e.started

	close(e.gate)
	e.disp.Wait()

	e.mu.Lock()
	assert.Len(t, e.handled, 2, "one handler per source+key, not one per signal")
	e.mu.Unlock()
	recs, err := e.store.ListSignals(context.Background(), e.dir)
	require.NoError(t, err)
	assert.Len(t, recs, 1+burst+1, "every signal is stored even when coalesced")

	// With the handler finished, the same source+key starts a new one.
	again := e.post(t, e.slug, auth, validSignal)
	assert.Contains(t, again.Body.String(), `"coalesced":false`)
}

func TestSignals_DisabledWithoutIntake(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("POST", "/api/projects/x/signals", strings.NewReader(validSignal))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// A source that sends a new key per event is refused once enough handlers run.
func TestSignals_HandlerLimitAnswers429(t *testing.T) {
	e := setupSignals(t)
	post := func(i int) int {
		body := fmt.Sprintf(`{"source":"noisy","key":"k-%d","severity":"low","title":"t"}`, i)
		return e.post(t, e.slug, "Bearer "+e.token, body).Code
	}
	for i := 0; i < 8; i++ {
		require.Equal(t, http.StatusAccepted, post(i))
	}
	assert.Equal(t, http.StatusTooManyRequests, post(8), "a ninth handler must not start")
	// A repeat of a running key still joins its handler.
	assert.Equal(t, http.StatusAccepted, post(0))
}
