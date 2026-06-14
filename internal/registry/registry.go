// Package registry is the compile-time database-driver registry.
//
// Drivers register themselves from their package init():
//
//	func init() { registry.Register(myDriver{}) }
//
// The application aggregates registered drivers via plugins/plugins_all.go
// (anonymous imports). Build tags on that file let us strip a driver from
// the binary (e.g. `go build -tags no_mysql`).
//
// See ARCHITECTURE.md §3.3 and CLAUDE.md #7.
package registry

import (
	"fmt"
	"sort"
	"sync"

	"catdb/internal/dbdriver"
)

var (
	mu      sync.RWMutex
	drivers = make(map[string]dbdriver.Driver)
)

// Register adds a Driver under its Name. Registering the same name twice
// indicates a programming error (two plugins claim the same id) — we panic
// here, at init() time, rather than picking a winner silently.
func Register(d dbdriver.Driver) {
	if d == nil {
		panic("dbdriver: Register called with nil Driver")
	}
	name := d.Name()
	if name == "" {
		panic("dbdriver: Register called with empty driver name")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := drivers[name]; dup {
		panic(fmt.Sprintf("dbdriver: driver %q registered twice", name))
	}
	drivers[name] = d
}

// Get returns the driver registered under name, or an error if none is.
func Get(name string) (dbdriver.Driver, error) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("dbdriver: no driver registered as %q", name)
	}
	return d, nil
}

// List returns all registered drivers sorted by Name (for the "new connection"
// dropdown). The returned slice is a snapshot and safe to mutate.
func List() []dbdriver.Driver {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]dbdriver.Driver, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names returns the registered driver names (for quick checks / logs).
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(drivers))
	for n := range drivers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// reset is used by tests to wipe the registry between cases. Not exported.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	drivers = make(map[string]dbdriver.Driver)
}
