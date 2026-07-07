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
	// "mysql" | "mariadb" | "postgresql" | "sqlite" | "mssql" | "standard".
	// Unknown ids fall back to "standard".
	EditorDialect string `json:"editorDialect"`

	// IdentQuote is the identifier quote character ("`", "\"", "[").
	IdentQuote string `json:"identQuote"`

	// StringBackslashEscapes: backslash escapes inside '…' string literals
	// (MySQL). Off for ANSI-conforming databases — a backslash is literal.
	StringBackslashEscapes bool `json:"stringBackslashEscapes,omitempty"`

	// SystemSchemas are catalog/system namespaces hidden from casual
	// completion until the user types a matching prefix.
	SystemSchemas []string `json:"systemSchemas,omitempty"`

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

// CharsetInfo is one server character set (DatabaseEditor).
type CharsetInfo struct {
	Name             string `json:"name"`
	DefaultCollation string `json:"defaultCollation,omitempty"`
}

// CollationInfo is one server collation (DatabaseEditor).
type CollationInfo struct {
	Name    string `json:"name"`
	Charset string `json:"charset,omitempty"`
}

// DatabaseOptions are the create/alter-database form values. Drivers map
// them onto their native concepts (MySQL: charset/collation; a Postgres
// driver may map Charset to encoding).
type DatabaseOptions struct {
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
}

// DatabaseEditor is an OPTIONAL extension a driver's Metadata may implement
// when the database supports creating/altering databases with options from
// the UI. The service layer probes for it via type assertion; drivers
// without it make the front-end hide the database editor's option fields.
type DatabaseEditor interface {
	ListCharsets(ctx context.Context) ([]CharsetInfo, error)
	ListCollations(ctx context.Context) ([]CollationInfo, error)
	GetDatabaseOptions(ctx context.Context, db string) (DatabaseOptions, error)
	// CreateDatabaseSQL/AlterDatabaseSQL render the DDL; opts fields the
	// dialect doesn't support are ignored.
	CreateDatabaseSQL(name string, opts DatabaseOptions) (string, error)
	AlterDatabaseSQL(name string, opts DatabaseOptions) (string, error)
}
