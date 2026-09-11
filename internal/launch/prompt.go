package launch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"catdb/internal/cli"
	"catdb/internal/services"
	"catdb/internal/storage"
)

// This file is the launch flow's authentication prompt (docs/CLI_CON_DESIGN.md
// §6.2): a `-con` that asks to connect but carries no password, for a
// connection the keyring has no password for, stops and asks the user before
// touching the network — which is what DBeaver does, and what the external
// launchers that drive that dialog through the Win32 API expect.
//
// The dialog itself is NOT this package's business: Windows shows a native one
// (wailsbridge), everywhere else the front-end shows a modal. Both arrive here
// through CredentialPrompter, which main() injects.

// promptTimeout bounds the wait for a human. Five minutes matches DBeaver's
// own patience and keeps a forgotten dialog from pinning the launch forever.
const promptTimeout = 5 * time.Minute

// PromptRequest carries what the dialog shows. Locale comes from -nl.
type PromptRequest struct {
	ConnectionName string
	User           string
	Locale         string
}

// CredentialPrompter asks the user for a connection password.
type CredentialPrompter interface {
	// PromptPassword blocks until the user (or an automation tool) supplies a
	// password or cancels. ok=false means cancelled/timeout.
	PromptPassword(ctx context.Context, req PromptRequest) (password string, ok bool)
}

// PrompterFunc adapts a plain function to CredentialPrompter.
type PrompterFunc func(ctx context.Context, req PromptRequest) (string, bool)

// PromptPassword implements CredentialPrompter.
func (f PrompterFunc) PromptPassword(ctx context.Context, req PromptRequest) (string, bool) {
	return f(ctx, req)
}

// SetPrompter installs the dialog the launch flow uses when a specification
// needs a password it was not given. nil (the default) disables prompting
// entirely: the connect attempt then proceeds without a password, exactly as
// it did before this existed.
func (l *Launcher) SetPrompter(p CredentialPrompter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prompter = p
}

func (l *Launcher) currentPrompter() CredentialPrompter {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.prompter
}

// needsPasswordPrompt decides whether a specification must ask for a password
// before connecting. Pure, so the rule is testable on its own:
//
//   - only when the spec actually wants a connection (openConsole implies one);
//   - never when the command line supplied a password — including the explicit
//     `password=` that clears it, which is a deliberate "no password";
//   - only when the keyring has nothing stored for the connection.
//
// savePassword plays no part here: it decides whether what the user types is
// KEPT, not whether we ask (DBeaver behaves the same — savePassword=false is
// precisely the flow the external launchers use).
func needsPasswordPrompt(spec cli.ConSpec, stored string) bool {
	if !spec.Connect && !spec.OpenConsole {
		return false
	}
	if spec.SetKeys["password"] {
		return false
	}
	return stored == ""
}

// willPrompt is needsPasswordPrompt against the live keyring for an existing
// profile. Used before patching to decide whether the "savePassword=false has
// no effect" note still applies — with a prompt ahead it does not: the
// password we are about to ask for really is dropped afterwards.
func (l *Launcher) willPrompt(spec cli.ConSpec, id string) bool {
	if l.currentPrompter() == nil {
		return false
	}
	return needsPasswordPrompt(spec, l.storedPassword(id))
}

// ensurePassword runs the prompt when the specification needs one and parks
// the answer in the keyring, because that is where session.Manager reads
// credentials from — catdb has no in-memory credential cache.
//
// proceed=false means the user cancelled (or the prompt timed out) and the
// caller must not connect; the error is already on the outcome. drop=true
// means the password just obtained is not to be remembered and the caller
// clears it once the specification is done.
func (l *Launcher) ensurePassword(ctx context.Context, spec cli.ConSpec, id, locale string, out *services.LaunchOutcome) (drop, proceed bool) {
	prompter := l.currentPrompter()
	if prompter == nil {
		return false, true
	}
	if !needsPasswordPrompt(spec, l.storedPassword(id)) {
		return false, true
	}

	// The saved profile is the truth for what the dialog shows: after a patch
	// it already carries the command line's name/user, and after a create it
	// carries the generated name.
	name, user := specLabel(spec), spec.Draft.User
	if prof, err := l.store.GetConnection(ctx, id); err == nil {
		name, user = prof.Name, prof.User
	}

	promptCtx, cancel := context.WithTimeout(ctx, promptTimeout)
	defer cancel()
	password, ok := prompter.PromptPassword(promptCtx, PromptRequest{
		ConnectionName: name, User: user, Locale: locale,
	})
	if !ok {
		out.Errors = append(out.Errors, services.LaunchError{Slug: "auth-cancelled", Name: name})
		return false, false
	}
	if err := l.setStoredPassword(id, password); err != nil {
		out.Errors = append(out.Errors, services.LaunchError{
			Slug: "save-failed", Name: name, Detail: err.Error(),
		})
		return false, false
	}
	return !spec.SavePassword, true
}

// storedPassword reads the keyring password for a connection. Every failure —
// no entry, or a keyring that cannot be read at all — reports "none stored":
// asking the user is the useful answer in both cases.
func (l *Launcher) storedPassword(id string) string {
	if id == "" {
		return ""
	}
	secret, err := l.secrets.Load(id)
	if err != nil {
		if !errors.Is(err, storage.ErrSecretNotFound) {
			log.Printf("launch: read stored credentials for %s: %v", id, err)
		}
		return ""
	}
	return secret.Password
}

// setStoredPassword writes ONLY the password field, preserving the SSH
// credentials already stored for the connection — unlike the create path's
// wholesale wipe, this runs against connections the user may have configured
// by hand.
func (l *Launcher) setStoredPassword(id, password string) error {
	secret, err := l.secrets.Load(id)
	if err != nil && !errors.Is(err, storage.ErrSecretNotFound) {
		return fmt.Errorf("read stored credentials: %w", err)
	}
	secret.Password = password
	if err := l.secrets.Save(id, secret); err != nil {
		return fmt.Errorf("store password: %w", err)
	}
	return nil
}

// clearStoredPassword undoes setStoredPassword for savePassword=false, again
// field-precisely: an SSH password or key passphrase the user stored earlier
// is none of this flow's business.
func (l *Launcher) clearStoredPassword(id string) {
	if err := l.setStoredPassword(id, ""); err != nil {
		log.Printf("launch: drop prompted password for %s: %v", id, err)
	}
}
