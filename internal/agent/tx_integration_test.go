//go:build integration

// Integration test for the agent task-transaction mode (§5 gate 5) against a
// real MySQL: plan → approval → DML inside the dedicated-connection tx →
// read-your-writes before commit → commit visibility, and the rollback path.
//
// Run locally (requires Docker):
//
//	go test -tags=integration ./internal/agent/...
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"catdb/internal/dbdriver"
	"catdb/internal/llm"
	"catdb/internal/llm/llmtest"
	"catdb/internal/registry"
	"catdb/internal/storage"
	_ "catdb/plugins/mysqldrv"
)

type txEnv struct {
	drv  dbdriver.Driver
	cfg  dbdriver.ConnConfig
	conn dbdriver.Connection // pooled connection used by the engine
	side dbdriver.Connection // independent connection for visibility checks
}

func newTxEnv(t *testing.T, ctx context.Context) *txEnv {
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
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatal(err)
	}
	drv, err := registry.Get("mysql")
	if err != nil {
		t.Fatal(err)
	}
	cfg := dbdriver.ConnConfig{Host: host, Port: int(port.Num()), User: "root", Password: "secret", Database: "test"}
	conn, err := drv.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	side, err := drv.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open side: %v", err)
	}
	t.Cleanup(func() { side.Close() })

	if _, err := conn.Querier().Exec(ctx, "CREATE TABLE t (id INT PRIMARY KEY, v VARCHAR(32))"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return &txEnv{drv: drv, cfg: cfg, conn: conn, side: side}
}

func (env *txEnv) countRows(t *testing.T, ctx context.Context) int {
	t.Helper()
	rs, err := env.side.Querier().Query(ctx, "SELECT COUNT(*) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	rows, _, err := rs.Next(1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("count: %v %v", rows, err)
	}
	switch v := rows[0][0].(type) {
	case int64:
		return int(v)
	case string:
		if v == "0" {
			return 0
		}
		return int(v[0] - '0')
	default:
		t.Fatalf("unexpected count type %T", v)
		return -1
	}
}

func newRealEngine(t *testing.T, env *txEnv, p llm.Provider, decisions []decision) (*Engine, *storage.Store, string) {
	t.Helper()
	store, err := storage.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	prof, err := store.SaveConnection(context.Background(), storage.ConnectionProfile{
		Name: "it", Driver: "mysql", Environment: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.CreateAgentSession(context.Background(), storage.AgentSession{
		ConnID: prof.ID, Mode: "agent", ProviderID: "p", Model: "m",
		Grants: []string{"select", "insert", "delete"},
	})
	if err != nil {
		t.Fatal(err)
	}

	e := &Engine{
		store:   store,
		resolve: func(ctx context.Context, id string) (llm.Provider, error) { return p, nil },
		connect: func(ctx context.Context, id string) (dbdriver.Connection, dbdriver.Driver, error) {
			return env.conn, env.drv, nil
		},
		dedicated: func(ctx context.Context, id string) (dbdriver.Connection, error) {
			return env.drv.Open(ctx, env.cfg)
		},
		broker:        newApprovalBroker(),
		txm:           newTxManager(),
		maxIterations: 25,
	}
	var mu sync.Mutex
	queue := append([]decision(nil), decisions...)
	e.emit = func(name string, data any) {
		if name != "agent:approval" && name != "agent:plan" {
			return
		}
		mu.Lock()
		if len(queue) == 0 {
			mu.Unlock()
			t.Errorf("unexpected %s, empty queue", name)
			return
		}
		d := queue[0]
		queue = queue[1:]
		mu.Unlock()
		m := data.(map[string]any)
		id, _ := m["approvalID"].(string)
		if id == "" {
			id, _ = m["planID"].(string)
		}
		go func() {
			if d.approve {
				_ = e.Approve(id, d.scope)
			} else {
				_ = e.Reject(id, d.reason)
			}
		}()
	}
	return e, store, sess.ID
}

func TestTaskTxCommitAgainstMySQL(t *testing.T) {
	ctx := context.Background()
	env := newTxEnv(t, ctx)
	p := llmtest.New("fake",
		toolRound("submit_plan", `{"goal":"insert one row","statements":["INSERT INTO t VALUES (1,'a')"]}`),
		toolRound("run_sql", `{"db":"test","sql":"INSERT INTO t VALUES (1,'a')"}`),
		toolRound("run_sql", `{"db":"test","sql":"SELECT v FROM t WHERE id = 1"}`),
		endRound("done"),
	)
	e, store, sessID := newRealEngine(t, env, p,
		[]decision{{approve: true, scope: scopeOnce}, {approve: true, scope: scopeOnce}})

	if err := e.Send(ctx, sessID, "插入一行", nil); err != nil {
		t.Fatal(err)
	}
	// Read-your-writes: the SELECT inside the open tx saw the uncommitted row.
	r := lastToolResult(t, store, sessID)
	if r.IsError || !strings.Contains(r.Content, "a") {
		t.Fatalf("in-tx read must see the uncommitted row, got %+v", r)
	}
	// Outside the tx nothing is visible yet.
	if n := env.countRows(t, ctx); n != 0 {
		t.Fatalf("row visible before commit: %d", n)
	}
	if err := e.CommitTx(ctx, sessID); err != nil {
		t.Fatal(err)
	}
	if n := env.countRows(t, ctx); n != 1 {
		t.Fatalf("row missing after commit: %d", n)
	}
	audits, _ := store.ListAgentAudit(ctx, storage.AgentAuditFilter{SessionID: sessID})
	var okInsert bool
	for _, a := range audits {
		if a.Class == "insert" && a.Status == "ok" {
			okInsert = true
		}
	}
	if !okInsert {
		t.Fatalf("insert audit missing: %+v", audits)
	}
}

func TestTaskTxRollbackAgainstMySQL(t *testing.T) {
	ctx := context.Background()
	env := newTxEnv(t, ctx)
	p := llmtest.New("fake",
		toolRound("submit_plan", `{"goal":"insert","statements":["INSERT INTO t VALUES (2,'b')"]}`),
		toolRound("run_sql", `{"db":"test","sql":"INSERT INTO t VALUES (2,'b')"}`),
		endRound("done"),
	)
	e, store, sessID := newRealEngine(t, env, p,
		[]decision{{approve: true, scope: scopeOnce}, {approve: true, scope: scopeOnce}})

	if err := e.Send(ctx, sessID, "插入", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.RollbackTx(ctx, sessID); err != nil {
		t.Fatal(err)
	}
	if n := env.countRows(t, ctx); n != 0 {
		t.Fatalf("rollback leaked a row: %d", n)
	}
	audits, _ := store.ListAgentAudit(ctx, storage.AgentAuditFilter{SessionID: sessID})
	var rolled bool
	for _, a := range audits {
		if a.Class == "insert" && a.Status == "rolled-back" {
			rolled = true
		}
	}
	if !rolled {
		t.Fatalf("rolled-back audit missing: %+v", audits)
	}
}
