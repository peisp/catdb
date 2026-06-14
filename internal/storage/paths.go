// Package storage owns the on-disk side of the app: connection profiles in
// SQLite and the matching passwords in the OS keyring. Nothing else should
// touch either of those directly — go through this package.
//
// Key rule (CLAUDE.md #8): passwords NEVER live in SQLite. They are written
// to the OS keyring keyed on the connection's UUID; SQLite only carries the
// non-secret fields.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

const appDirName = "catdb"

// AppConfigDir returns the platform-appropriate user-config dir for catdb and
// ensures it exists. Used for the SQLite database, future preferences, etc.
//
//	macOS:   ~/Library/Application Support/catdb
//	Linux:   $XDG_CONFIG_HOME/catdb  (or ~/.config/catdb)
//	Windows: %AppData%\catdb
func AppConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("storage: locate user config dir: %w", err)
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("storage: create %s: %w", dir, err)
	}
	return dir, nil
}

// DefaultDBPath is the path of the SQLite file under AppConfigDir.
func DefaultDBPath() (string, error) {
	dir, err := AppConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "catdb.db"), nil
}
