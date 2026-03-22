package sqlite

import (
	"context"
	"testing"
)

func newTestStoreKV(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSetAndGetContextKey(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	if err := s.SetContextKey(ctx, "task1", "attempt1", "branch", "feature-x"); err != nil {
		t.Fatalf("SetContextKey: %v", err)
	}

	val, found, err := s.GetContextKey(ctx, "task1", "attempt1", "branch")
	if err != nil {
		t.Fatalf("GetContextKey: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if val != "feature-x" {
		t.Errorf("GetContextKey = %q, want %q", val, "feature-x")
	}
}

func TestGetContextKey_MissingKey(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	_, found, err := s.GetContextKey(ctx, "task1", "attempt1", "nonexistent")
	if err != nil {
		t.Fatalf("GetContextKey: %v", err)
	}
	if found {
		t.Error("expected key to be absent")
	}
}

func TestSetContextKey_Overwrites(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	_ = s.SetContextKey(ctx, "task1", "attempt1", "k", "v1")
	_ = s.SetContextKey(ctx, "task1", "attempt1", "k", "v2")

	val, _, _ := s.GetContextKey(ctx, "task1", "attempt1", "k")
	if val != "v2" {
		t.Errorf("GetContextKey = %q, want %q", val, "v2")
	}
}

func TestSetContextKey_NamespaceIsolation(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	_ = s.SetContextKey(ctx, "task1", "attempt1", "key", "a1")
	_ = s.SetContextKey(ctx, "task1", "attempt2", "key", "a2")

	v1, _, _ := s.GetContextKey(ctx, "task1", "attempt1", "key")
	v2, _, _ := s.GetContextKey(ctx, "task1", "attempt2", "key")
	if v1 != "a1" {
		t.Errorf("attempt1 key = %q, want a1", v1)
	}
	if v2 != "a2" {
		t.Errorf("attempt2 key = %q, want a2", v2)
	}
}

func TestListContextKeys(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	_ = s.SetContextKey(ctx, "task1", "attempt1", "b", "2")
	_ = s.SetContextKey(ctx, "task1", "attempt1", "a", "1")
	_ = s.SetContextKey(ctx, "task1", "attempt1", "c", "3")

	keys, err := s.ListContextKeys(ctx, "task1", "attempt1")
	if err != nil {
		t.Fatalf("ListContextKeys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	// Keys are returned sorted alphabetically.
	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Errorf("keys = %v, want [a b c]", keys)
	}
}

func TestDeleteContextKeys(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	_ = s.SetContextKey(ctx, "task1", "attempt1", "x", "1")
	_ = s.SetContextKey(ctx, "task1", "attempt1", "y", "2")
	// Different attempt — must not be deleted.
	_ = s.SetContextKey(ctx, "task1", "attempt2", "z", "3")

	if err := s.DeleteContextKeys(ctx, "task1", "attempt1"); err != nil {
		t.Fatalf("DeleteContextKeys: %v", err)
	}

	_, found1, _ := s.GetContextKey(ctx, "task1", "attempt1", "x")
	_, found2, _ := s.GetContextKey(ctx, "task1", "attempt1", "y")
	_, found3, _ := s.GetContextKey(ctx, "task1", "attempt2", "z")

	if found1 || found2 {
		t.Error("expected attempt1 keys to be deleted")
	}
	if !found3 {
		t.Error("expected attempt2 key to survive")
	}
}

func TestSeedAutoKeys(t *testing.T) {
	s := newTestStoreKV(t)
	ctx := context.Background()

	// Simulate the auto-seed the executor does at run start.
	pairs := [][2]string{
		{"task_id", "task1"},
		{"attempt_id", "attempt1"},
		{"workflow", "develop"},
		{"run_id", "task1-develop"},
	}
	for _, p := range pairs {
		if err := s.SetContextKey(ctx, "task1", "attempt1", p[0], p[1]); err != nil {
			t.Fatalf("SetContextKey %q: %v", p[0], err)
		}
	}

	for _, p := range pairs {
		val, found, err := s.GetContextKey(ctx, "task1", "attempt1", p[0])
		if err != nil || !found || val != p[1] {
			t.Errorf("key %q: got (%q, %v, %v), want (%q, true, nil)", p[0], val, found, err, p[1])
		}
	}
}
