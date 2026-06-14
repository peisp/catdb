package dbdriver

import (
	"context"
	"database/sql"
)

// Driver is the entry point for one database type (e.g. "mysql", "postgres").
// Plugins implement Driver and register themselves via init() →
// internal/registry.Register(). See ARCHITECTURE.md §3.4 for the steps to add
// a new driver.
type Driver interface {
	// Name is the unique identifier (e.g. "mysql"). Must be stable; used as the
	// key in the registry and persisted in connection configs.
	Name() string

	// Version is the driver build version (for the "About" dialog / logs).
	Version() string

	// ConnectionSchema is consumed by the front-end to render the connection
	// form. Adding a new database does not require front-end changes.
	ConnectionSchema() []ConnParamField

	// Capabilities lets the UI hide unsupported features (e.g. transactions on
	// ClickHouse, schemas on MySQL).
	Capabilities() Capabilities

	// Dialect returns the per-database SQL quirks (identifier quoting,
	// pagination syntax, type mapping, CREATE TABLE generation).
	Dialect() Dialect

	// Open establishes a Connection (which encapsulates the pool). ctx applies
	// to the dial + initial handshake.
	Open(ctx context.Context, cfg ConnConfig) (Connection, error)
}

// Connection is an established connection (pool) to one database server.
// The pool itself is plugin-internal; the abstraction only exposes the
// operations the core layer needs.
type Connection interface {
	Ping(ctx context.Context) error
	Close() error

	Querier() Querier
	Metadata() Metadata
	Editor() Editor

	// Begin starts a transaction. The returned Tx is also a Querier so all
	// query/exec ops route through the same physical connection while the
	// transaction is in flight.
	Begin(ctx context.Context, opts *sql.TxOptions) (Tx, error)
}

// Querier runs SQL on a Connection or Tx.
//
// All methods MUST honor ctx — long-running queries must be cancellable from
// the front-end (see ARCHITECTURE.md §4.2 and §6.1).
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (ExecResult, error)
	Query(ctx context.Context, sql string, args ...any) (ResultSet, error)
	Explain(ctx context.Context, sql string) (ResultSet, error)
}

// ResultSet is the streaming-batch reader for a SELECT result.
//
// Never load all rows at once: the core layer calls Next(batch) repeatedly and
// streams them up through the Service layer. Row data is [][]any (NOT
// []map[string]any) — see ARCHITECTURE.md §6.1 for the rationale.
type ResultSet interface {
	// Columns is the column metadata. Sent to the front-end ONCE per query.
	Columns() []ColumnMeta

	// Next fetches the next batch of rows. done=true means there are no more
	// rows; rows may still be non-empty in the final batch.
	Next(batch int) (rows [][]any, done bool, err error)

	Close() error
}

// Metadata serves the object tree / SQL completion source. All reads must be
// ctx-aware so the front-end can cancel a slow information_schema scan.
type Metadata interface {
	ListDatabases(ctx context.Context) ([]string, error)
	ListSchemas(ctx context.Context, db string) ([]string, error)
	ListTables(ctx context.Context, db, schema string) ([]TableInfo, error)
	ListViews(ctx context.Context, db, schema string) ([]ViewInfo, error)
	ListColumns(ctx context.Context, db, schema, table string) ([]ColumnMeta, error)
	ListIndexes(ctx context.Context, db, schema, table string) ([]IndexInfo, error)
	ListForeignKeys(ctx context.Context, db, schema, table string) ([]ForeignKeyInfo, error)
	ListRoutines(ctx context.Context, db, schema string) ([]RoutineInfo, error)

	// GetCreateTable returns the database's native CREATE TABLE text — e.g.
	// the result of MySQL's `SHOW CREATE TABLE`. Used by the structure
	// viewer; cheaper and more accurate than reconstructing from columns.
	GetCreateTable(ctx context.Context, db, schema, table string) (string, error)
}

// Dialect describes per-database SQL quirks.
type Dialect interface {
	// QuoteIdentifier wraps an identifier (table/column) for the target
	// database (MySQL `name`, Postgres "name", SQL Server [name], …).
	QuoteIdentifier(name string) string

	// Paginate wraps baseSQL with a database-appropriate LIMIT/OFFSET clause.
	Paginate(baseSQL string, limit, offset int) string

	// MapType maps a native database type name to the logical type used by
	// the front-end.
	MapType(nativeType string) LogicalType

	// GenerateCreateTable emits a CREATE TABLE statement for the given schema.
	GenerateCreateTable(t TableSchema) (string, error)
}

// Editor builds parameterized write statements for the row-edit feature.
//
// Rule (see CLAUDE.md #4): UPDATE/DELETE MUST be keyed on a primary or unique
// key. Tables with no such key are flagged read-only by the core layer; no
// write statement is produced for them.
type Editor interface {
	// PrimaryKeys returns the column names of the table's primary key (or its
	// chosen unique key, if no PK exists). Empty slice → table is read-only.
	PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error)

	BuildInsert(table string, row map[string]any) (sql string, args []any, err error)
	BuildUpdate(table string, pk, changes map[string]any) (sql string, args []any, err error)
	BuildDelete(table string, pk map[string]any) (sql string, args []any, err error)
}

// Tx is an open transaction. It is also a Querier so the same Exec/Query
// methods work whether or not the caller is in a transaction.
type Tx interface {
	Querier
	Commit() error
	Rollback() error
}
