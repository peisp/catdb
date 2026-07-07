package storage

import (
	"context"
	"path/filepath"
	"testing"

	"catdb/internal/dbdriver"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSaveAndListConnections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, err := s.SaveConnection(ctx, ConnectionProfile{
		Name:     "local mysql",
		Driver:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "root",
		Database: "test",
		Params:   map[string]string{"timeout": "5s"},
		SSL:      &dbdriver.SSLConfig{Mode: "disable"},
	})
	if err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}
	if p.ID == "" {
		t.Fatal("ID should be assigned")
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatal("timestamps should be set")
	}

	got, err := s.GetConnection(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.Name != p.Name || got.Host != p.Host || got.SSL == nil || got.SSL.Mode != "disable" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	list, err := s.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("expected 1 connection, got %d", len(list))
	}
}

func TestUpdateConnection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "n", Driver: "mysql", Host: "a", Port: 1, User: "u"})
	p.Host = "b"
	p.Port = 2
	got, err := s.SaveConnection(ctx, p)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Host != "b" || got.Port != 2 {
		t.Fatalf("update did not stick: %+v", got)
	}
	if !got.UpdatedAt.After(got.CreatedAt) && !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Fatal("UpdatedAt should advance")
	}
}

func TestDeleteConnection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "n", Driver: "mysql", Host: "h", Port: 1, User: "u"})
	if err := s.DeleteConnection(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteConnection(ctx, p.ID); err == nil {
		t.Fatal("delete twice should error")
	}
}

func TestGroupCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	g, err := s.SaveGroup(ctx, Group{Name: "Prod"})
	if err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}
	if g.ID == "" {
		t.Fatal("group ID should be assigned")
	}

	groups, err := s.ListGroups(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("ListGroups: got %d, err=%v", len(groups), err)
	}

	if err := s.DeleteGroup(ctx, g.ID); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
}

func TestSavedQueryCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	conn, err := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	if err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}

	q, err := s.SaveSavedQuery(ctx, SavedQuery{
		ConnID:  conn.ID,
		DBName:  "shop",
		Name:    "active users",
		SQLText: "SELECT * FROM users WHERE active = 1",
	})
	if err != nil {
		t.Fatalf("SaveSavedQuery: %v", err)
	}
	if q.ID == "" || q.CreatedAt.IsZero() || q.UpdatedAt.IsZero() {
		t.Fatalf("id/timestamps should be set: %+v", q)
	}

	// Scope filtering: a different db or schema sees nothing.
	if list, err := s.ListSavedQueries(ctx, conn.ID, "other", ""); err != nil || len(list) != 0 {
		t.Fatalf("expected empty for other db, got %d (err=%v)", len(list), err)
	}
	if list, err := s.ListSavedQueries(ctx, conn.ID, "shop", "public"); err != nil || len(list) != 0 {
		t.Fatalf("expected empty for other schema, got %d (err=%v)", len(list), err)
	}
	list, err := s.ListSavedQueries(ctx, conn.ID, "shop", "")
	if err != nil || len(list) != 1 || list[0].ID != q.ID {
		t.Fatalf("ListSavedQueries: got %d (err=%v)", len(list), err)
	}

	// Update keeps id, changes name + sql.
	q.Name = "active users v2"
	q.SQLText = "SELECT id FROM users"
	upd, err := s.SaveSavedQuery(ctx, q)
	if err != nil {
		t.Fatalf("update SaveSavedQuery: %v", err)
	}
	got, _ := s.ListSavedQueries(ctx, conn.ID, "shop", "")
	if len(got) != 1 || got[0].Name != "active users v2" || got[0].SQLText != "SELECT id FROM users" {
		t.Fatalf("update mismatch: %+v", got)
	}
	_ = upd

	// Delete.
	if err := s.DeleteSavedQuery(ctx, q.ID); err != nil {
		t.Fatalf("DeleteSavedQuery: %v", err)
	}
	if err := s.DeleteSavedQuery(ctx, q.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestSavedQueryCascadeOnConnectionDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	conn, _ := s.SaveConnection(ctx, ConnectionProfile{Name: "c", Driver: "mysql"})
	if _, err := s.SaveSavedQuery(ctx, SavedQuery{ConnID: conn.ID, DBName: "d", Name: "n", SQLText: "SELECT 1"}); err != nil {
		t.Fatalf("SaveSavedQuery: %v", err)
	}
	if err := s.DeleteConnection(ctx, conn.ID); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	list, err := s.ListSavedQueries(ctx, conn.ID, "d", "")
	if err != nil || len(list) != 0 {
		t.Fatalf("expected cascade delete, got %d (err=%v)", len(list), err)
	}
}
