//go:build !production

package agenttrace

// Enabled gates the whole trace subsystem: dev builds record full
// model-interaction traces, production builds compile every Rec call into a
// no-op and the Trace window entry is hidden.
const Enabled = true
