package storage

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Secrets is the OS-keyring half of the persistence layer. Use one Secrets
// per app instance; the service name is what the user sees in Keychain Access
// / Credential Manager / Secret Service.
type Secrets struct{ service string }

// NewSecrets returns a Secrets that stores under the given service name
// (default: the build-tag appName — "catdb" in production, "catdb-dev" in dev).
func NewSecrets(service string) *Secrets {
	if service == "" {
		service = appName
	}
	return &Secrets{service: service}
}

// connSecret is the JSON blob stored in the keyring for a single connection.
// All passwords live here — DB password, SSH password, private-key passphrase.
type connSecret struct {
	Password       string `json:"password,omitempty"`
	SSHPassword    string `json:"sshPassword,omitempty"`
	SSHKeyPassword string `json:"sshKeyPassword,omitempty"`
}

// ErrSecretNotFound is returned when no entry exists for the given ID.
var ErrSecretNotFound = errors.New("storage: secret not found")

// Save writes the per-connection secret blob, or deletes it if empty.
func (s *Secrets) Save(id string, secret Secret) error {
	if id == "" {
		return fmt.Errorf("storage: empty connection id")
	}
	blob := connSecret{
		Password:       secret.Password,
		SSHPassword:    secret.SSHPassword,
		SSHKeyPassword: secret.SSHKeyPassword,
	}
	if blob == (connSecret{}) {
		_ = keyring.Delete(s.service, id)
		return nil
	}
	data, err := json.Marshal(blob)
	if err != nil {
		return fmt.Errorf("storage: marshal secret: %w", err)
	}
	if err := keyring.Set(s.service, id, string(data)); err != nil {
		return fmt.Errorf("storage: keyring.Set: %w", err)
	}
	return nil
}

// Load returns the stored secret for a connection. Missing entries return
// ErrSecretNotFound; the caller can treat that as "no password set".
func (s *Secrets) Load(id string) (Secret, error) {
	data, err := keyring.Get(s.service, id)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return Secret{}, ErrSecretNotFound
		}
		return Secret{}, fmt.Errorf("storage: keyring.Get: %w", err)
	}
	var blob connSecret
	if err := json.Unmarshal([]byte(data), &blob); err != nil {
		// Tolerate the legacy "plain password" form if it ever shows up.
		return Secret{Password: data}, nil
	}
	return Secret{
		Password:       blob.Password,
		SSHPassword:    blob.SSHPassword,
		SSHKeyPassword: blob.SSHKeyPassword,
	}, nil
}

// Delete removes the secret. Idempotent.
func (s *Secrets) Delete(id string) error {
	if err := keyring.Delete(s.service, id); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("storage: keyring.Delete: %w", err)
	}
	return nil
}

// Secret is the in-memory shape returned/accepted by Secrets.
type Secret struct {
	Password       string `json:"password,omitempty"`
	SSHPassword    string `json:"sshPassword,omitempty"`
	SSHKeyPassword string `json:"sshKeyPassword,omitempty"`
}
