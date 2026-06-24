package services

import (
	"context"
	"fmt"

	"catdb/internal/storage"
)

// SavedQueryService is the Wails Service that owns named SQL snippets stored
// under a connection's database node in the object tree. Per ARCHITECTURE.md
// §2 it stays THIN: validates input, forwards to storage.
type SavedQueryService struct {
	store *storage.Store
}

// NewSavedQueryService wires the storage dependency.
func NewSavedQueryService(store *storage.Store) *SavedQueryService {
	return &SavedQueryService{store: store}
}

func (s *SavedQueryService) ServiceName() string { return "SavedQueryService" }

// List returns the saved queries scoped to a connection + database.
func (s *SavedQueryService) List(ctx context.Context, connID, db string) ([]storage.SavedQuery, error) {
	if connID == "" {
		return nil, fmt.Errorf("SavedQueryService: connID is required")
	}
	return s.store.ListSavedQueries(ctx, connID, db)
}

// Save inserts (empty ID) or updates a saved query and returns the persisted row.
func (s *SavedQueryService) Save(ctx context.Context, draft storage.SavedQuery) (storage.SavedQuery, error) {
	return s.store.SaveSavedQuery(ctx, draft)
}

// Delete removes a saved query by ID.
func (s *SavedQueryService) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("SavedQueryService: id is required")
	}
	return s.store.DeleteSavedQuery(ctx, id)
}
