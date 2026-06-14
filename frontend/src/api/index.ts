// frontend/src/api — the front-end anti-corruption layer.
//
// Components MUST go through this module and NEVER import from
// `bindings/` or `@wailsio/runtime` directly. Rationale (CLAUDE.md #1):
// Wails v3 is still alpha; centralising every binding/event call here keeps
// breaking changes contained to a single layer.
//
// Add a new wrapper here whenever a component needs a Service method or an
// event. The wrapper should be paper-thin — input/output passthrough plus,
// where useful, AbortSignal → promise cancellation.
//
// API surface so far:
//   demo       — end-to-end M0 verification (greet / long-task / progress)

export * as connections from './connections'
export * as query from './query'
export * as metadata from './metadata'
export * as edit from './edit'
export * as transfer from './transfer'
export * as system from './system'
export * from './events'
