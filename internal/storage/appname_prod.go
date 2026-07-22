//go:build production

package storage

// appName namespaces all on-disk/on-keyring app data: the config dir
// (~/Library/Application Support/<appName>) and the keyring service name.
const appName = "catdb"
