package dbdriver

import (
	"context"
	"errors"
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

	// UIDialect is the declarative UI descriptor (identifier quoting, editor
	// dialect, type system, completion catalogs). Like ConnectionSchema, it is
	// consumed by the front-end so new databases need no front-end changes.
	UIDialect() UIDialect

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

	// ServerInfo returns runtime metadata (server version, current user).
	ServerInfo(ctx context.Context) (ServerInfo, error)

	Querier() Querier
	Metadata() Metadata
	Editor() Editor

	// Begin starts a transaction. The returned Tx is also a Querier so all
	// query/exec ops route through the same physical connection while the
	// transaction is in flight. opts may be nil (driver defaults).
	Begin(ctx context.Context, opts *TxOptions) (Tx, error)
}

// DatabaseRouter is an OPTIONAL extension for drivers whose databases are
// hard isolation boundaries (PostgreSQL): one session cannot execute SQL
// against a sibling database, so the driver maintains per-database pools
// internally. Generic layers that are about to run SQL addressed at a
// specific database resolve their Querier/Tx through RouteQuerier/RouteBegin,
// which probe for this extension; drivers without it (MySQL — every database
// is reachable from one session) fall back to Connection.Querier()/Begin().
type DatabaseRouter interface {
	// QuerierFor returns a Querier whose session database is db.
	QuerierFor(ctx context.Context, db string) (Querier, error)
	// BeginFor starts a transaction on db.
	BeginFor(ctx context.Context, db string, opts *TxOptions) (Tx, error)
}

// RouteQuerier resolves the Querier for SQL addressed at db. db=="" always
// means the connection's default database.
func RouteQuerier(ctx context.Context, conn Connection, db string) (Querier, error) {
	if r, ok := conn.(DatabaseRouter); ok && db != "" {
		return r.QuerierFor(ctx, db)
	}
	q := conn.Querier()
	if q == nil {
		return nil, errors.New("dbdriver: connection has no querier")
	}
	return q, nil
}

// RouteBegin starts a transaction on db (see RouteQuerier).
func RouteBegin(ctx context.Context, conn Connection, db string, opts *TxOptions) (Tx, error) {
	if r, ok := conn.(DatabaseRouter); ok && db != "" {
		return r.BeginFor(ctx, db, opts)
	}
	return conn.Begin(ctx, opts)
}

// StatementClassifier is an OPTIONAL extension a driver's Dialect may
// implement to override classification of dialect-specific statements the
// generic lexical classifier can't know (PG's COPY, MySQL's LOAD DATA).
// Returning Class ClassUnknown hands the statement back to the generic
// classifier. Probed by type assertion, same pattern as BulkMetadata.
type StatementClassifier interface {
	ClassifyStatement(sql string) StatementClassification
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

	// ListViewDefinitions returns the definition body of every view in
	// (db, schema), keyed by view name. Used by structure sync to compare
	// views in one round-trip. Drivers without views return an empty map.
	ListViewDefinitions(ctx context.Context, db, schema string) (map[string]string, error)
	ListColumns(ctx context.Context, db, schema, table string) ([]ColumnMeta, error)
	ListIndexes(ctx context.Context, db, schema, table string) ([]IndexInfo, error)
	ListForeignKeys(ctx context.Context, db, schema, table string) ([]ForeignKeyInfo, error)
	ListRoutines(ctx context.Context, db, schema string) ([]RoutineInfo, error)

	// GetCreateTable returns the database's native CREATE TABLE text — e.g.
	// the result of MySQL's `SHOW CREATE TABLE`. Used by the structure
	// viewer; cheaper and more accurate than reconstructing from columns.
	GetCreateTable(ctx context.Context, db, schema, table string) (string, error)
}

// BulkMetadata is an OPTIONAL extension a driver's Metadata may implement:
// whole-schema reads in ONE query per aspect, keyed by table name. Structure
// sync probes for it via type assertion — with N tables this turns ~3N
// per-table information_schema round-trips into 3, which dominates compare
// latency on remote/tunneled connections. Drivers that don't implement it
// are served by the per-table Metadata methods transparently.
type BulkMetadata interface {
	ListAllColumns(ctx context.Context, db, schema string) (map[string][]ColumnMeta, error)
	ListAllIndexes(ctx context.Context, db, schema string) (map[string][]IndexInfo, error)
	ListAllForeignKeys(ctx context.Context, db, schema string) (map[string][]ForeignKeyInfo, error)
}

// Dialect describes per-database SQL quirks.
type Dialect interface {
	// QuoteIdentifier wraps an identifier (table/column) for the target
	// database (MySQL `name`, Postgres "name", SQL Server [name], …).
	QuoteIdentifier(name string) string

	// DefaultNamespaceSQL returns the statement that makes name the session's
	// default namespace, so unqualified identifiers in subsequent statements
	// resolve against it — the database for MySQL ("USE `x`"), the schema for
	// Postgres ("SET search_path TO \"x\""). The dialect quotes name itself.
	// An empty return means the database has no such statement.
	DefaultNamespaceSQL(name string) string

	// ScriptRules returns the lexical rules for splitting this database's
	// SQL scripts into statements (see core/sqlscript).
	ScriptRules() ScriptRules

	// Placeholder returns the parameter placeholder for the i-th argument
	// (1-based) of a parameterized statement — "?" for MySQL, "$1"…"$n" for
	// Postgres. Generic layers use it to build multi-value parameterized SQL
	// (e.g. batch INSERTs) without literal-encoding values themselves.
	Placeholder(i int) string

	// Paginate wraps baseSQL with a database-appropriate LIMIT/OFFSET clause.
	Paginate(baseSQL string, limit, offset int) string

	// MapType maps a native database type name to the logical type used by
	// the front-end.
	MapType(nativeType string) LogicalType

	// NormalizeType canonicalizes a native type string for equality
	// comparison in schema diffing, folding this database's cosmetic
	// variations (case, param whitespace, MySQL's UNSIGNED position /
	// ZEROFILL noise, …) so equivalent types compare equal.
	NormalizeType(nativeType string) string

	// GenerateCreateTable emits a CREATE TABLE statement for the given schema.
	GenerateCreateTable(t TableSchema) (string, error)

	// GenerateAlterTable renders a schemadiff ChangeSet into DDL statements
	// for the (db, schema, table) target, in safe execution order. An empty
	// ChangeSet yields an empty slice.
	GenerateAlterTable(db, schema, table string, cs ChangeSet) ([]string, error)
}

// Editor builds parameterized write statements for the row-edit feature.
//
// Rule (see CLAUDE.md #4): UPDATE/DELETE MUST be keyed on a primary or unique
// key. Tables with no such key are flagged read-only by the core layer; no
// write statement is produced for them.
//
// All methods address the table as (db, schema, table) — the driver decides
// how to qualify and quote it (MySQL ignores schema, Postgres ignores db).
type Editor interface {
	// PrimaryKeys returns the column names of the table's primary key (or its
	// chosen unique key, if no PK exists). Empty slice → table is read-only.
	PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error)

	BuildInsert(db, schema, table string, row map[string]any) (sql string, args []any, err error)
	BuildUpdate(db, schema, table string, pk, changes map[string]any) (sql string, args []any, err error)
	BuildDelete(db, schema, table string, pk map[string]any) (sql string, args []any, err error)
}

// Tx is an open transaction. It is also a Querier so the same Exec/Query
// methods work whether or not the caller is in a transaction.
type Tx interface {
	Querier
	Commit() error
	Rollback() error
}

// QualifyTable renders a fully qualified, quoted table reference from its
// (db, schema, table) parts, skipping empty ones. MySQL passes schema=""
// → `db`.`table`; schema-ful databases get all three levels.
func QualifyTable(d Dialect, db, schema, table string) string {
	out := ""
	for _, part := range []string{db, schema, table} {
		if part == "" {
			continue
		}
		if out != "" {
			out += "."
		}
		out += d.QuoteIdentifier(part)
	}
	return out
}
