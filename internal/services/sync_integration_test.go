//go:build integration

// Integration tests for structure & data synchronization: one real MySQL
// (testcontainers) hosting two databases that act as source and target,
// reached through two separate driver connections. Same code paths as two
// servers — the sync pipeline only ever sees two dbdriver.Connection values.
//
// Run locally:
//
//	go test -tags=integration ./internal/services/...
//
// Requires Docker.
package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	_ "catdb/plugins/mysqldrv"
)

type syncEnv struct {
	drv     dbdriver.Driver
	cfg     dbdriver.ConnConfig
	admin   dbdriver.Connection
	srcConn dbdriver.Connection
	tgtConn dbdriver.Connection
}

func newSyncEnv(t *testing.T, ctx context.Context) *syncEnv {
	t.Helper()
	container, err := tcmysql.Run(ctx, "mysql:8.0",
		tcmysql.WithDatabase("test"),
		tcmysql.WithUsername("root"),
		tcmysql.WithPassword("secret"),
	)
	if err != nil {
		t.Fatalf("start mysql container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	drv, err := registry.Get("mysql")
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	cfg := dbdriver.ConnConfig{Host: host, Port: int(port.Num()), User: "root", Password: "secret", Database: "test"}

	open := func(db string) dbdriver.Connection {
		c := cfg
		c.Database = db
		conn, err := drv.Open(ctx, c)
		if err != nil {
			t.Fatalf("open %s: %v", db, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}

	env := &syncEnv{drv: drv, cfg: cfg}
	env.admin = open("test")
	mustExecSQL(t, ctx, env.admin, "CREATE DATABASE src_db")
	mustExecSQL(t, ctx, env.admin, "CREATE DATABASE tgt_db")
	env.srcConn = open("src_db")
	env.tgtConn = open("tgt_db")
	return env
}

func mustExecSQL(t *testing.T, ctx context.Context, c dbdriver.Connection, sql string) {
	t.Helper()
	if _, err := c.Querier().Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func fetchRows(t *testing.T, ctx context.Context, c dbdriver.Connection, sql string) [][]any {
	t.Helper()
	rs, err := c.Querier().Query(ctx, sql)
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	defer rs.Close()
	var out [][]any
	for {
		rows, done, err := rs.Next(500)
		if err != nil {
			t.Fatalf("next %q: %v", sql, err)
		}
		out = append(out, rows...)
		if done {
			return out
		}
	}
}

func TestStructureSyncE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env := newSyncEnv(t, ctx)

	// Source shape.
	mustExecSQL(t, ctx, env.srcConn, `CREATE TABLE src_db.t_common (
		id INT NOT NULL PRIMARY KEY,
		name VARCHAR(64) NOT NULL,
		extra INT NULL,
		INDEX idx_name (name)
	)`)
	mustExecSQL(t, ctx, env.srcConn, `CREATE TABLE src_db.t_only_src (
		id INT NOT NULL PRIMARY KEY,
		v VARCHAR(32) NULL
	)`)
	mustExecSQL(t, ctx, env.srcConn, "CREATE VIEW src_db.v_users AS SELECT id, name FROM src_db.t_common")

	// Target shape: t_common differs (narrow name, extra legacy column, no
	// index), t_only_tgt is extra, the view selects fewer columns.
	mustExecSQL(t, ctx, env.tgtConn, `CREATE TABLE tgt_db.t_common (
		id INT NOT NULL PRIMARY KEY,
		name VARCHAR(32) NOT NULL,
		legacy TEXT NULL
	)`)
	mustExecSQL(t, ctx, env.tgtConn, `CREATE TABLE tgt_db.t_only_tgt (
		id INT NOT NULL PRIMARY KEY
	)`)
	mustExecSQL(t, ctx, env.tgtConn, "CREATE VIEW tgt_db.v_users AS SELECT id FROM tgt_db.t_common")

	req := SchemaCompareRequest{SourceDB: "src_db", TargetDB: "tgt_db"}
	res, err := compareSchemasConns(ctx, env.srcConn, env.tgtConn, env.drv, env.drv, req)
	if err != nil {
		t.Fatalf("CompareSchemas: %v", err)
	}
	byName := map[string]SchemaObjectDiff{}
	for _, o := range res.Objects {
		byName[o.Kind+":"+o.Name] = o
		if o.Error != "" {
			t.Fatalf("object %s/%s errored: %s", o.Kind, o.Name, o.Error)
		}
	}
	if d := byName["table:t_common"]; d.Status != "alter" || !d.Destructive {
		t.Fatalf("t_common: want destructive alter, got %+v", d)
	}
	if d := byName["table:t_only_src"]; d.Status != "create" || len(d.Statements) != 1 {
		t.Fatalf("t_only_src: want create, got %+v", d)
	}
	if d := byName["table:t_only_tgt"]; d.Status != "drop" || !d.Destructive {
		t.Fatalf("t_only_tgt: want destructive drop, got %+v", d)
	}
	if d := byName["view:v_users"]; d.Status != "alter" {
		t.Fatalf("v_users: want alter, got %+v", d)
	}

	// Apply everything (drops included) and expect convergence.
	var stmts []string
	for _, o := range res.Objects {
		stmts = append(stmts, o.Statements...)
	}
	execRes, err := executeSchemaStatements(ctx, env.tgtConn, SchemaSyncExecRequest{Statements: stmts, StopOnError: true})
	if err != nil {
		t.Fatalf("ExecuteSchemaSync: %v", err)
	}
	if execRes.Failed != 0 {
		t.Fatalf("execution failures: %+v", execRes.Results)
	}

	res2, err := compareSchemasConns(ctx, env.srcConn, env.tgtConn, env.drv, env.drv, req)
	if err != nil {
		t.Fatalf("re-compare: %v", err)
	}
	for _, o := range res2.Objects {
		if o.Status != "same" {
			t.Errorf("after sync, %s/%s still %q: %v (err=%s)", o.Kind, o.Name, o.Status, o.Statements, o.Error)
		}
	}
}

func TestDataSyncE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env := newSyncEnv(t, ctx)

	ddl := `CREATE TABLE %s.d1 (
		id INT NOT NULL PRIMARY KEY,
		val VARCHAR(32) NULL,
		num DECIMAL(10,2) NULL
	)`
	mustExecSQL(t, ctx, env.srcConn, fmt.Sprintf(ddl, "src_db"))
	mustExecSQL(t, ctx, env.tgtConn, fmt.Sprintf(ddl, "tgt_db"))
	// A keyless table on both sides must be reported as skipped, not synced.
	mustExecSQL(t, ctx, env.srcConn, "CREATE TABLE src_db.nokey (a INT NULL)")
	mustExecSQL(t, ctx, env.tgtConn, "CREATE TABLE tgt_db.nokey (a INT NULL)")

	mustExecSQL(t, ctx, env.srcConn, "INSERT INTO src_db.d1 VALUES (1,'a',1.50),(2,'b',NULL),(4,'d',4.00)")
	mustExecSQL(t, ctx, env.tgtConn, "INSERT INTO tgt_db.d1 VALUES (2,'STALE',NULL),(3,'c',3.00),(4,'d',4.00)")

	compareReq := DataCompareRequest{SourceDB: "src_db", TargetDB: "tgt_db"}
	res, err := compareDataConns(ctx, env.srcConn, env.tgtConn, env.drv, env.drv, compareReq)
	if err != nil {
		t.Fatalf("CompareData: %v", err)
	}
	byName := map[string]DataTableDiff{}
	for _, d := range res.Tables {
		byName[d.Table] = d
	}
	d1 := byName["d1"]
	if d1.Inserts != 1 || d1.Updates != 1 || d1.Deletes != 1 {
		t.Fatalf("d1 counts = %d/%d/%d, want 1/1/1", d1.Inserts, d1.Updates, d1.Deletes)
	}
	if len(d1.Samples) != 3 {
		t.Fatalf("d1 samples = %+v", d1.Samples)
	}
	if nk := byName["nokey"]; nk.Skipped != "no-primary-key" {
		t.Fatalf("nokey: want skipped no-primary-key, got %+v", nk)
	}

	// Pass 1: AllowDelete=false — row 3 must survive on the target.
	writeConn, err := env.drv.Open(ctx, func() dbdriver.ConnConfig { c := env.cfg; c.Database = "tgt_db"; return c }())
	if err != nil {
		t.Fatalf("open dedicated: %v", err)
	}
	defer writeConn.Close()

	execReq := DataSyncExecRequest{
		SourceDB: "src_db", TargetDB: "tgt_db",
		Tables: []string{"d1"}, AllowDelete: false, BatchSize: 2,
	}
	execRes, err := executeDataSyncConns(ctx, env.srcConn, env.tgtConn, writeConn, env.drv, env.drv, execReq)
	if err != nil {
		t.Fatalf("ExecuteDataSync: %v", err)
	}
	if e := execRes.Tables[0]; e.Error != "" || e.Inserts != 1 || e.Updates != 1 {
		t.Fatalf("exec pass 1: %+v", e)
	}
	rows := fetchRows(t, ctx, env.tgtConn, "SELECT id, val FROM tgt_db.d1 ORDER BY id")
	if len(rows) != 4 {
		t.Fatalf("target after no-delete sync: want 4 rows (extra row kept), got %d", len(rows))
	}

	// Pass 2: AllowDelete=true — full convergence.
	execReq.AllowDelete = true
	if _, err := executeDataSyncConns(ctx, env.srcConn, env.tgtConn, writeConn, env.drv, env.drv, execReq); err != nil {
		t.Fatalf("ExecuteDataSync (delete): %v", err)
	}
	res2, err := compareDataConns(ctx, env.srcConn, env.tgtConn, env.drv, env.drv, compareReq)
	if err != nil {
		t.Fatalf("re-compare: %v", err)
	}
	for _, d := range res2.Tables {
		if d.Table == "d1" && d.Inserts+d.Updates+d.Deletes != 0 {
			t.Fatalf("d1 not converged: %+v", d)
		}
	}
	rows = fetchRows(t, ctx, env.tgtConn, "SELECT id, val FROM tgt_db.d1 ORDER BY id")
	if len(rows) != 3 {
		t.Fatalf("target after full sync: want 3 rows, got %d", len(rows))
	}
}
