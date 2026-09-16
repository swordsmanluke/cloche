package sqlite_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	_ "modernc.org/sqlite"
)

// delayingDriver wraps another driver.Driver, sleeping before every
// QueryContext call issued against connections it opens. Used to give reads
// a measurable, fixed cost so concurrency (or the lack of it) shows up as a
// deterministic difference in wall-clock time instead of a flaky race.
type delayingDriver struct {
	underlying driver.Driver
	delay      time.Duration
}

func (d *delayingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.underlying.Open(name)
	if err != nil {
		return nil, err
	}
	return &delayingConn{Conn: c, delay: d.delay}, nil
}

// delayingConn must implement driver.QueryerContext/ExecerContext itself
// (not just promote the underlying conn's methods via embedding) so the
// delay actually runs instead of being bypassed by promotion.
type delayingConn struct {
	driver.Conn
	delay time.Duration
}

func (c *delayingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	time.Sleep(c.delay)
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, fmt.Errorf("underlying conn does not implement QueryerContext")
	}
	return qc.QueryContext(ctx, query, args)
}

func (c *delayingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, fmt.Errorf("underlying conn does not implement ExecerContext")
	}
	return ec.ExecContext(ctx, query, args)
}

// seedRuns creates n runs for projectDir, so ListRunsByProject has
// something to scan.
func seedRuns(t *testing.T, store *sqlite.Store, projectDir string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		run := domain.NewRun(fmt.Sprintf("seed-%d", i), "wf")
		run.ProjectDir = projectDir
		run.Start()
		require.NoError(t, store.CreateRun(ctx, run))
	}
}

// TestListRunsByProject_ConcurrentReadsDoNotSerialize proves that N
// concurrent ListRunsByProject calls run in parallel against the read pool
// rather than queuing behind each other on a single connection: each read
// is artificially delayed by a fixed amount, so if they were serialized N
// concurrent calls would take roughly N * delay, but with the pool they
// should complete in roughly the time of one.
func TestListRunsByProject_ConcurrentReadsDoNotSerialize(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cloche.db")

	base, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	underlying := base.Driver()
	require.NoError(t, base.Close())

	const delay = 150 * time.Millisecond
	driverName := fmt.Sprintf("sqlite-delay-%d", time.Now().UnixNano())
	sql.Register(driverName, &delayingDriver{underlying: underlying, delay: delay})

	write, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	write.SetMaxOpenConns(1)

	read, err := sql.Open(driverName, dbPath)
	require.NoError(t, err)
	read.SetMaxOpenConns(8)

	store, err := sqlite.NewStoreWithDBs(write, read)
	require.NoError(t, err)
	defer store.Close()

	seedRuns(t, store, "/proj", 5)

	const n = 6
	ctx := context.Background()
	errs := make([]error, n)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.ListRunsByProject(ctx, "/proj", time.Time{})
			errs[i] = err
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Less(t, elapsed, delay*time.Duration(n)/2,
		"reads appear to be serialized: %d concurrent reads (each artificially delayed %s) took %s", n, delay, elapsed)
}

// TestConcurrentReadsAndWrite_NoDeadlock exercises the real NewStore
// construction (write pool + read pool sharing one file) with reads and a
// write firing concurrently, asserting the mix completes without deadlocking
// and without errors — i.e. the single write connection and the read pool
// coexist safely under WAL.
func TestConcurrentReadsAndWrite_NoDeadlock(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	seedRuns(t, store, "/proj", 5)

	ctx := context.Background()
	const readers = 10
	const writers = 5
	errCh := make(chan error, readers+writers)
	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.ListRunsByProject(ctx, "/proj", time.Time{})
			errCh <- err
		}()
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run := domain.NewRun(fmt.Sprintf("concurrent-%d", i), "wf")
			run.ProjectDir = "/proj"
			run.Start()
			errCh <- store.CreateRun(ctx, run)
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent reads/write did not complete in time — possible deadlock")
	}
	close(errCh)
	for err := range errCh {
		assert.NoError(t, err)
	}
}
