package services

import (
	"context"
	"log"

	"catdb/internal/storage"
)

// This file holds the service-layer half of the command-line launch flow
// (docs/CLI_CON_DESIGN.md §4.4): the result shape the front-end consumes, the
// hook SystemService exposes to drain a cold-start request, and the ephemeral
// housekeeping.
//
// The flow's ENGINE lives in internal/launch, not here: internal/cli already
// imports this package for ConnectionDraft, so services cannot import cli back.
// internal/launch is the one package that sees both, and it hands its result
// back in the types declared below so the Wails bindings stay in services.

// LaunchError is one user-visible failure from a `-con` specification. Slug is
// a stable identifier the front-end maps to `error.launch.<slug>` (CLAUDE.md's
// i18n rule: no localized text crosses the Go boundary); Name is the
// connection the failure concerns and Detail the raw technical message, both
// fed to the message as {name} / {error}.
type LaunchError struct {
	Slug   string `json:"slug"`
	Name   string `json:"name,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// LaunchOutcome is what the front-end needs to act on a processed command
// line: which connections to show, which ones asked for a SQL console, and
// what went wrong.
type LaunchOutcome struct {
	// ConnectionIDs are the profiles located or created, in spec order.
	ConnectionIDs []string `json:"connectionIds"`
	// ConsoleIDs is the subset whose spec carried openConsole=true.
	ConsoleIDs []string `json:"consoleIds"`
	// Errors are the user-visible failures. Parser/translation warnings are
	// NOT here — those are technical English and go to the log.
	Errors []LaunchError `json:"errors"`
}

// PendingLaunch is the cold-start hand-off. Wails drops events emitted before
// a window is ready, so a command line seen during startup is parked and the
// front-end pulls it on mount instead. Implemented by *launch.Launcher.
type PendingLaunch interface {
	ConsumePendingLaunch(ctx context.Context) LaunchOutcome
}

// CleanupEphemeralConnections deletes every ephemeral profile and its keyring
// entry. Run at startup (sweeping leftovers from a crash or a kill -9) and
// again from ConnectionService.ServiceShutdown. Returns the number removed.
//
// A package function rather than a Service method on purpose: Wails binds
// every exported method, and this is not something the front-end may trigger.
func CleanupEphemeralConnections(ctx context.Context, store *storage.Store, secrets *storage.Secrets) int {
	ids, err := store.DeleteEphemeralConnections(ctx)
	if err != nil {
		log.Printf("launch: sweep ephemeral connections: %v", err)
		return 0
	}
	for _, id := range ids {
		if err := secrets.Delete(id); err != nil {
			log.Printf("launch: drop keyring entry for ephemeral connection %s: %v", id, err)
		}
	}
	return len(ids)
}

// ServiceShutdown drops the connections the command line created with
// `save=false`. Wails v3 lifecycle naming (CLAUDE.md #2).
func (s *ConnectionService) ServiceShutdown() error {
	if n := CleanupEphemeralConnections(context.Background(), s.store, s.secrets); n > 0 {
		log.Printf("launch: removed %d ephemeral connection(s) on shutdown", n)
	}
	return nil
}
