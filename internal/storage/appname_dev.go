//go:build !production

package storage

// Dev builds (wails3 dev / plain go build, no -tags production) keep their
// config dir and keyring entries fully separate from a production install so
// development never reads or clobbers real user data.
const appName = "catdb-dev"
