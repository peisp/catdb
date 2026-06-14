// Package dbdriver defines the database driver abstraction (the load-bearing wall).
//
// Rules (see ARCHITECTURE.md §3):
//   - Interfaces do NOT depend on database/sql concrete types. ResultSet/ColumnMeta/ExecResult
//     are framework-agnostic so plugins are free to use *sql.DB, pgx native, etc.
//   - Capabilities() drives UI feature visibility.
//   - ConnectionSchema() drives dynamic connection-form rendering.
//
// Any change here must be propagated to every plugin and to the contract test suite.
package dbdriver

// ConnParamField describes one field of a driver's connection form.
// The front-end renders the form dynamically from this list; adding a new
// database does not require a front-end change.
type ConnParamField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | number | password | select | bool
	Default  string   `json:"default,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
	Group    string   `json:"group,omitempty"` // "常规" | "SSL" | "SSH"
	Help     string   `json:"help,omitempty"`
}

// Capabilities lets the UI hide features the underlying database does not support.
type Capabilities struct {
	Schemas          bool `json:"schemas"`
	StoredProcedures bool `json:"storedProcedures"`
	Triggers         bool `json:"triggers"`
	Views            bool `json:"views"`
	Transactions     bool `json:"transactions"`
	ExplainPlan      bool `json:"explainPlan"`
}

// SSLConfig is the framework-agnostic SSL/TLS profile passed to drivers.
type SSLConfig struct {
	Mode       string `json:"mode"` // disable | prefer | require | verify-ca | verify-full
	CACert     string `json:"caCert,omitempty"`
	ClientCert string `json:"clientCert,omitempty"`
	ClientKey  string `json:"clientKey,omitempty"`
	ServerName string `json:"serverName,omitempty"`
}

// ServerInfo holds runtime metadata about a database server — what you get
// from `SELECT VERSION(), USER()` in MySQL. Populated on connect and cached
// by the front-end store for the status bar.
type ServerInfo struct {
	Version string `json:"version"` // e.g. "8.0.32"
	User    string `json:"user"`    // e.g. "root@localhost"
}

// SSHConfig describes an SSH jump tunnel. Auth is mutually exclusive:
// password OR private key OR ssh-agent.
type SSHConfig struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	User           string `json:"user"`
	Password       string `json:"password,omitempty"`
	PrivateKey     string `json:"privateKey,omitempty"`
	PrivateKeyPass string `json:"privateKeyPass,omitempty"`
	UseAgent       bool   `json:"useAgent,omitempty"`
	KnownHostsPath string `json:"knownHostsPath,omitempty"` // for FixedHostKey verification
}

// ConnConfig is the framework-agnostic connection profile.
// Plugins translate Params into their own DSN/options.
type ConnConfig struct {
	Host      string            `json:"host"`
	Port      int               `json:"port"`
	User      string            `json:"user"`
	Password  string            `json:"password,omitempty"`
	Database  string            `json:"database,omitempty"`
	Params    map[string]string `json:"params,omitempty"` // driver-specific: charset, loc, tls, etc.
	SSL       *SSLConfig        `json:"ssl,omitempty"`
	SSHTunnel *SSHConfig        `json:"sshTunnel,omitempty"`
}

// LogicalType is the cross-database column type after Dialect.MapType normalization.
type LogicalType string

const (
	TypeUnknown   LogicalType = "unknown"
	TypeBool      LogicalType = "bool"
	TypeInt       LogicalType = "int"
	TypeBigInt    LogicalType = "bigint"
	TypeFloat     LogicalType = "float"
	TypeDecimal   LogicalType = "decimal"
	TypeString    LogicalType = "string"
	TypeText      LogicalType = "text"
	TypeBytes     LogicalType = "bytes"
	TypeJSON      LogicalType = "json"
	TypeDate      LogicalType = "date"
	TypeTime      LogicalType = "time"
	TypeDateTime  LogicalType = "datetime"
	TypeTimestamp LogicalType = "timestamp"
	TypeUUID      LogicalType = "uuid"
	TypeEnum      LogicalType = "enum"
)

// ColumnMeta is the column descriptor returned by a ResultSet — sent to the
// front-end once per query (NOT per row) to keep IPC payloads small.
type ColumnMeta struct {
	Name           string      `json:"name"`
	NativeType     string      `json:"nativeType"` // e.g. "VARCHAR", "BIGINT", "DATETIME(6)"
	LogicalType    LogicalType `json:"logicalType"`
	Nullable       bool        `json:"nullable"`
	Length         int64       `json:"length,omitempty"`
	Precision      int64       `json:"precision,omitempty"`
	Scale          int64       `json:"scale,omitempty"`
	Default        *string     `json:"default,omitempty"`
	IsPrimaryKey   bool        `json:"isPrimaryKey,omitempty"`
	IsAutoIncrement bool       `json:"isAutoIncrement,omitempty"`
	Comment        string      `json:"comment,omitempty"`
}

// ExecResult is returned by non-SELECT statements.
type ExecResult struct {
	RowsAffected int64 `json:"rowsAffected"`
	LastInsertID int64 `json:"lastInsertId"`
}

// TableInfo is a row in the object tree at the table level.
type TableInfo struct {
	Name    string `json:"name"`
	Schema  string `json:"schema,omitempty"`
	Engine  string `json:"engine,omitempty"`
	Comment string `json:"comment,omitempty"`
	Rows    int64  `json:"rows,omitempty"`
}

// ViewInfo is a view in the object tree.
type ViewInfo struct {
	Name    string `json:"name"`
	Schema  string `json:"schema,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// IndexInfo describes one index on a table.
type IndexInfo struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	Type    string   `json:"type,omitempty"` // BTREE, HASH, FULLTEXT, ...
}

// ForeignKeyInfo describes one FK constraint.
type ForeignKeyInfo struct {
	Name              string   `json:"name"`
	Columns           []string `json:"columns"`
	ReferencedSchema  string   `json:"referencedSchema,omitempty"`
	ReferencedTable   string   `json:"referencedTable"`
	ReferencedColumns []string `json:"referencedColumns"`
	OnUpdate          string   `json:"onUpdate,omitempty"`
	OnDelete          string   `json:"onDelete,omitempty"`
}

// RoutineInfo covers stored procedures, functions, and triggers.
type RoutineInfo struct {
	Name       string `json:"name"`
	Schema     string `json:"schema,omitempty"`
	Type       string `json:"type"` // PROCEDURE | FUNCTION | TRIGGER
	Definition string `json:"definition,omitempty"`
	Comment    string `json:"comment,omitempty"`
}

// TableSchema is the input to Dialect.GenerateCreateTable.
type TableSchema struct {
	Name        string         `json:"name"`
	Schema      string         `json:"schema,omitempty"`
	Columns     []ColumnMeta   `json:"columns"`
	PrimaryKey  []string       `json:"primaryKey,omitempty"`
	Indexes     []IndexInfo    `json:"indexes,omitempty"`
	ForeignKeys []ForeignKeyInfo `json:"foreignKeys,omitempty"`
	Engine      string         `json:"engine,omitempty"`
	Charset     string         `json:"charset,omitempty"`
	Comment     string         `json:"comment,omitempty"`
}
