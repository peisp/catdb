package services

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"

	"catdb/internal/storage"
	"catdb/wailsbridge"
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

// LaunchPasswordBroker is the front-end half of the launch-time authentication
// prompt (docs/CLI_CON_DESIGN.md §6.2): where Windows shows a native dialog an
// external launcher can drive, every other platform — and any user who simply
// typed a `-con` without a password — gets an in-app modal.
//
// It is a rendezvous, nothing more: Prompt emits the request and parks on a
// channel, SubmitLaunchPassword hands the answer back over it. Requests are
// keyed so a late or duplicate answer cannot land on the wrong one.
type LaunchPasswordBroker struct {
	mu      sync.Mutex
	seq     uint64
	waiting map[string]chan launchPasswordReply
}

type launchPasswordReply struct {
	password string
	ok       bool
}

// NewLaunchPasswordBroker returns a ready broker. One per process.
func NewLaunchPasswordBroker() *LaunchPasswordBroker {
	return &LaunchPasswordBroker{waiting: map[string]chan launchPasswordReply{}}
}

// Prompt emits "connection:password-request" {requestId, name, user} via
// wailsbridge.Emit and blocks until SubmitLaunchPassword or ctx done.
//
// ok=false covers every way of not getting a password: the user cancelled, the
// caller's deadline expired, or no window was listening at all (Emit drops
// events with no app — the caller's timeout then ends the wait).
func (b *LaunchPasswordBroker) Prompt(ctx context.Context, name, user string) (string, bool) {
	id, ch := b.register()
	defer b.forget(id)

	wailsbridge.Emit("connection:password-request", map[string]any{
		"requestId": id,
		"name":      name,
		"user":      user,
	})

	select {
	case reply := <-ch:
		return reply.password, reply.ok
	case <-ctx.Done():
		return "", false
	}
}

// Submit delivers an answer to the waiting Prompt. Reports whether a request
// with that id was still waiting — a false means the prompt had already been
// answered or had given up, and the answer is discarded.
func (b *LaunchPasswordBroker) Submit(requestID, password string, ok bool) bool {
	b.mu.Lock()
	ch, waiting := b.waiting[requestID]
	if waiting {
		delete(b.waiting, requestID)
	}
	b.mu.Unlock()
	if !waiting {
		return false
	}
	// Buffered on registration, so this never blocks even if Prompt has just
	// lost the race to its own ctx.
	ch <- launchPasswordReply{password: password, ok: ok}
	return true
}

func (b *LaunchPasswordBroker) register() (string, chan launchPasswordReply) {
	ch := make(chan launchPasswordReply, 1)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := "launch-pw-" + strconv.FormatUint(b.seq, 10)
	b.waiting[id] = ch
	return id, ch
}

func (b *LaunchPasswordBroker) forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiting, id)
}

// SubmitLaunchPassword answers a pending `connection:password-request`. The
// front-end calls it for every outcome of its modal — confirm, cancel, close —
// so a dismissed dialog releases the blocked launch instead of leaking it
// until the five-minute timeout.
//
// ok=false means "no password, cancelled". An unknown requestId is an error
// with a stable slug; the front-end has nothing useful to do with it beyond
// logging, since the prompt it answers is already gone.
func (s *SystemService) SubmitLaunchPassword(_ context.Context, requestID, password string, ok bool) error {
	if s.passwords == nil {
		return fmt.Errorf("no-prompt-broker")
	}
	if !s.passwords.Submit(requestID, password, ok) {
		return fmt.Errorf("unknown-request")
	}
	return nil
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
