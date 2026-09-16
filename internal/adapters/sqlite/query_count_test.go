package sqlite_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
)

// countingDriver wraps another driver.Driver, counting every QueryContext
// call issued against connections it opens. Used to assert that a Store
// method issues a fixed number of round trips regardless of how much data
// it returns, rather than one query per row.
type countingDriver struct {
	underlying driver.Driver
	count      *int64
}

func (d *countingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.underlying.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: c, count: d.count}, nil
}

// countingConn wraps a driver.Conn, counting QueryContext calls. It must
// implement driver.QueryerContext itself (not just promote the underlying
// conn's method via embedding) so database/sql routes queries through it
// instead of falling back to Prepare+Query.
type countingConn struct {
	driver.Conn
	count *int64
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	atomic.AddInt64(c.count, 1)
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, fmt.Errorf("underlying conn does not implement QueryerContext")
	}
	return qc.QueryContext(ctx, query, args)
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, fmt.Errorf("underlying conn does not implement ExecerContext")
	}
	return ec.ExecContext(ctx, query, args)
}

// newCountingStore opens a Store backed by a fresh in-memory database routed
// through countingDriver, registering a uniquely-named driver so the test
// can run more than once per process.
func newCountingStore(t *testing.T) (*sqlite.Store, *int64) {
	t.Helper()

	base, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	underlying := base.Driver()
	require.NoError(t, base.Close())

	var count int64
	driverName := fmt.Sprintf("sqlite-counting-%d", time.Now().UnixNano())
	sql.Register(driverName, &countingDriver{underlying: underlying, count: &count})

	db, err := sql.Open(driverName, ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)

	store, err := sqlite.NewStoreWithDB(db)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	return store, &count
}

// TestListTasks_ConstantQueryCount asserts that Store.ListTasks issues the
// same number of queries whether the project has a handful of tasks or many
// more — i.e. attempts are loaded via a single joined query rather than one
// ListAttempts call per task.
func TestListTasks_ConstantQueryCount(t *testing.T) {
	store, count := newCountingStore(t)
	ctx := context.Background()

	seedTasks := func(n int, prefix string) {
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s-%d", prefix, i)
			require.NoError(t, store.SaveTask(ctx, &domain.Task{
				ID:         id,
				Title:      "Task " + id,
				Source:     domain.TaskSourceExternal,
				ProjectDir: "/proj",
				CreatedAt:  time.Now(),
			}))
			require.NoError(t, store.SaveAttempt(ctx, &domain.Attempt{
				ID:        id + "-a1",
				TaskID:    id,
				StartedAt: time.Now(),
				Result:    domain.AttemptResultSucceeded,
			}))
		}
	}

	seedTasks(5, "small")
	atomic.StoreInt64(count, 0)
	small, err := store.ListTasks(ctx, "/proj")
	require.NoError(t, err)
	require.Len(t, small, 5)
	smallQueries := atomic.LoadInt64(count)
	require.Greater(t, smallQueries, int64(0), "ListTasks should issue at least one query")

	seedTasks(100, "large")
	atomic.StoreInt64(count, 0)
	large, err := store.ListTasks(ctx, "/proj")
	require.NoError(t, err)
	require.Len(t, large, 105)
	largeQueries := atomic.LoadInt64(count)

	assert.Equal(t, smallQueries, largeQueries,
		"ListTasks issued %d queries for 5 tasks but %d queries for 105 tasks — attempts must be loaded via a single joined query, not one per task",
		smallQueries, largeQueries)
	assert.LessOrEqual(t, largeQueries, int64(3), "ListTasks should issue only a small, constant number of queries")

	for _, task := range large {
		require.Len(t, task.Attempts, 1)
	}
}

// TestGetTask_ConstantQueryCount is a smaller companion check: GetTask
// already loads a single task's attempts with one query, unaffected by how
// many other tasks/attempts exist in the store.
func TestGetTask_ConstantQueryCount(t *testing.T) {
	store, count := newCountingStore(t)
	ctx := context.Background()

	require.NoError(t, store.SaveTask(ctx, &domain.Task{
		ID: "t1", Title: "T1", Source: domain.TaskSourceExternal, ProjectDir: "/proj", CreatedAt: time.Now(),
	}))
	for i := 0; i < 10; i++ {
		require.NoError(t, store.SaveAttempt(ctx, &domain.Attempt{
			ID:        fmt.Sprintf("t1-a%d", i),
			TaskID:    "t1",
			StartedAt: time.Now(),
			Result:    domain.AttemptResultSucceeded,
		}))
	}

	atomic.StoreInt64(count, 0)
	got, err := store.GetTask(ctx, "t1")
	require.NoError(t, err)
	assert.Len(t, got.Attempts, 10)
	assert.LessOrEqual(t, atomic.LoadInt64(count), int64(2))
}
