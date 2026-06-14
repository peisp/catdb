// Package plugins anonymously imports every database driver plugin so that
// their init() Register calls populate the registry at startup.
//
// One driver per file so each can be stripped independently with build tags
// (e.g. `go build -tags no_mysql`).
//
// New driver checklist (see ARCHITECTURE.md §3.4):
//  1. Add plugins/<name>drv/ implementing dbdriver.Driver and registering in init().
//  2. Add plugins/plugins_<name>.go with `//go:build !no_<name>` and a blank import.
//  3. Make the contract test suite pass for the new driver.
package plugins
