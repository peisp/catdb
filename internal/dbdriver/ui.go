package dbdriver

import "context"

// UIDialect is the driver's declarative UI descriptor: everything the
// front-end must know about this database's SQL surface that is not runtime
// metadata — identifier quoting, editor dialect, the structure editor's type
// system, completion catalogs. Shipped once per driver via
// ConnectionService.ListDrivers, so adding a database never means editing
// front-end components.
//
// i18n rule: all Key fields are stable identifiers the front-end translates
// (with the raw key as fallback); free-text fields (Info, Detail) are English
// technical reference text shown as-is.
type UIDialect struct {
	// EditorDialect selects the CodeMirror SQL dialect by id:
	// "mysql" | "mariadb" | "postgresql" | "sqlite" | "mssql" | "plsql" |
	// "standard". Unknown ids fall back to "standard".
	EditorDialect string `json:"editorDialect"`

	// IdentQuote is the identifier quote character ("`", "\"", "[").
	IdentQuote string `json:"identQuote"`

	// StringBackslashEscapes: backslash escapes inside '…' string literals
	// (MySQL). Off for ANSI-conforming databases — a backslash is literal.
	StringBackslashEscapes bool `json:"stringBackslashEscapes,omitempty"`

	// SystemSchemas are catalog/system namespaces hidden from casual
	// completion until the user types a matching prefix.
	SystemSchemas []string `json:"systemSchemas,omitempty"`

	// DefaultSchema is the namespace new objects land in when the user gives
	// none (Postgres "public"). Empty for drivers without a schema level.
	DefaultSchema string `json:"defaultSchema,omitempty"`

	// Keywords are dialect-specific keywords/phrases offered by completion in
	// addition to the front-end's generic ANSI set.
	Keywords []string `json:"keywords,omitempty"`

	// Functions is the completion catalog of built-in functions.
	Functions []UIFunction `json:"functions,omitempty"`

	// Snippets are dialect-specific completion snippets. Body uses CodeMirror
	// snippet syntax (${placeholder}).
	Snippets []UISnippet `json:"snippets,omitempty"`

	// TypeGroups is the grouped catalog of column base types for the
	// structure editor's type dropdown. The first type of the first group is
	// the default for newly-added columns.
	TypeGroups []UITypeGroup `json:"typeGroups,omitempty"`

	// TypeFormats describes, per base type (uppercase), how its params field
	// behaves. Types absent from the map take {Kind:"none"}.
	TypeFormats map[string]UITypeFormat `json:"typeFormats,omitempty"`

	// DefaultColumnType/Params seed a newly-added column row
	// (MySQL: VARCHAR / 255).
	DefaultColumnType   string `json:"defaultColumnType,omitempty"`
	DefaultColumnParams string `json:"defaultColumnParams,omitempty"`

	// HasUnsigned reports whether the dialect has an UNSIGNED column modifier
	// at all (drives the column-editor toggle column visibility).
	HasUnsigned bool `json:"hasUnsigned,omitempty"`

	// AutoIncrement describes the dialect's auto-increment column rules.
	AutoIncrement UIAutoIncrement `json:"autoIncrement"`

	// PrimaryKeyForcesNotNull: ticking PK forces NOT NULL in the editor.
	PrimaryKeyForcesNotNull bool `json:"primaryKeyForcesNotNull,omitempty"`

	// IndexTypes are the selectable index methods (MySQL: BTREE/HASH/FULLTEXT;
	// Postgres: btree/hash/gin/gist). Empty hides the selector.
	IndexTypes []string `json:"indexTypes,omitempty"`

	// DefaultCharset preselects the database editor's charset picker
	// (MySQL: utf8mb4). Empty leaves the picker blank.
	DefaultCharset string `json:"defaultCharset,omitempty"`
}

// UIFunction is one completion catalog entry.
type UIFunction struct {
	Name string `json:"name"`
	// Category is a stable key ("aggregate", "string", "numeric", "datetime",
	// "control", "json", "cast", "system") shown as the completion detail.
	Category string `json:"category,omitempty"`
	// Info is an optional English one-liner (signature/summary).
	Info string `json:"info,omitempty"`
	// Params are the snippet placeholders; "…" marks variadic tails and is
	// skipped on insert. Nil with NoArgs=false inserts "fn(${})".
	Params []string `json:"params,omitempty"`
	// NoArgs functions insert as "fn()".
	NoArgs bool `json:"noArgs,omitempty"`
}

// UISnippet is one completion snippet.
type UISnippet struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Body   string `json:"body"`
}

// UITypeGroup is one group in the structure editor's type dropdown. Key is a
// stable identifier ("string", "integer", "decimal", "datetime", "binary",
// "boolean", "other") the front-end localizes.
type UITypeGroup struct {
	Key   string   `json:"key"`
	Types []string `json:"types"`
}

// UITypeFormat describes the params field behavior for one base type.
type UITypeFormat struct {
	// Kind is one of "length", "displayWidth", "precisionScale",
	// "fractionalSeconds", "enumValues", "none".
	Kind string `json:"kind"`
	// SupportsUnsigned enables the UNSIGNED toggle for this type.
	SupportsUnsigned bool `json:"supportsUnsigned,omitempty"`
	// ParamsRequired marks the params field mandatory (e.g. VARCHAR length).
	ParamsRequired bool `json:"paramsRequired,omitempty"`
}

// UIAutoIncrement describes auto-increment column rules for the editor.
type UIAutoIncrement struct {
	// Supported: the dialect has an auto-increment column flag at all.
	Supported bool `json:"supported,omitempty"`
	// BaseTypes restricts the flag to these base types (empty = any).
	BaseTypes []string `json:"baseTypes,omitempty"`
	// MaxPerTable caps flagged columns per table (0 = unlimited).
	MaxPerTable int `json:"maxPerTable,omitempty"`
}

// DatabaseOptionField describes one field of the create/alter-database form.
// Like ConnParamField, the front-end renders the form dynamically from this
// list, so each driver exposes its own native concepts (MySQL:
// charset/collation; Postgres: owner/template/encoding/lc_collate/lc_ctype/
// tablespace) without front-end changes. Every field is a select; choices
// are server-derived at DatabaseOptionFields time.
type DatabaseOptionField struct {
	// Key is the stable identifier used in the options map and for front-end
	// label localization (databaseEditor.field.*), with Label as fallback.
	Key   string `json:"key"`
	Label string `json:"label"` // English baseline

	// Options are the selectable values. When DependsOn is set, use OptionsBy
	// keyed by the parent field's current value instead.
	Options []string `json:"options,omitempty"`

	// DependsOn narrows the choices by another field's value (MySQL:
	// collation depends on charset). OptionsBy maps parent value → choices;
	// DefaultBy maps parent value → the value to snap to when the parent
	// changes and the current pick no longer belongs.
	DependsOn string              `json:"dependsOn,omitempty"`
	OptionsBy map[string][]string `json:"optionsBy,omitempty"`
	DefaultBy map[string]string   `json:"defaultBy,omitempty"`

	// Default preselects the field in create mode. Empty = leave blank
	// (server/template default applies).
	Default string `json:"default,omitempty"`

	// FixedOnAlter marks fields the database cannot change after creation
	// (Postgres: encoding/collation/template). The editor disables them in
	// edit mode and never passes them to AlterDatabaseSQL.
	FixedOnAlter bool `json:"fixedOnAlter,omitempty"`
}

// DatabaseEditor is an OPTIONAL extension a driver's Metadata may implement
// when the database supports creating/altering databases with options from
// the UI. The service layer probes for it via type assertion; drivers
// without it make the front-end report the feature as unsupported.
//
// The options map is keyed by DatabaseOptionField.Key; empty/absent values
// mean "omit the clause" (server default applies).
type DatabaseEditor interface {
	// DatabaseOptionFields returns the form descriptor with server-derived
	// choices resolved (charset/collation lists, roles, tablespaces, …).
	DatabaseOptionFields(ctx context.Context) ([]DatabaseOptionField, error)

	// GetDatabaseOptions returns db's current option values (edit prefill).
	GetDatabaseOptions(ctx context.Context, db string) (map[string]string, error)

	// CreateDatabaseSQL renders the CREATE DATABASE DDL from the full form
	// values; keys the dialect doesn't recognize are ignored.
	CreateDatabaseSQL(name string, opts map[string]string) (string, error)

	// AlterDatabaseSQL renders ALTER DDL from the CHANGED options only (the
	// front-end diffs against GetDatabaseOptions). Statements may be
	// newline-joined. Errors when a changed option cannot be altered.
	AlterDatabaseSQL(name string, opts map[string]string) (string, error)
}
