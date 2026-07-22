package agent

import (
	"context"
	"testing"
	"time"

	"catdb/internal/dbdriver"
)

// TestGateMatrix exhaustively sweeps environment × class × grants ×
// missing-where and asserts every invariant of gates 1/2/3/5 (§15 安全矩阵).
func TestGateMatrix(t *testing.T) {
	envs := []string{"", "dev", "test", "staging", "prod"}
	grantSets := []map[string]bool{
		{},               // read-only session (default)
		{"insert": true}, // single verb
		{"insert": true, "update": true, "delete": true, "ddl": true}, // all
	}
	classes := []dbdriver.StatementClassification{
		{Class: dbdriver.ClassRead, Verb: "select"},
		{Class: dbdriver.ClassRead, Verb: "show"},
		{Class: dbdriver.ClassWriteDML, Verb: "insert"},
		{Class: dbdriver.ClassWriteDML, Verb: "update"},
		{Class: dbdriver.ClassWriteDML, Verb: "update", MissingWhere: true},
		{Class: dbdriver.ClassWriteDML, Verb: "delete"},
		{Class: dbdriver.ClassWriteDML, Verb: "delete", MissingWhere: true},
		{Class: dbdriver.ClassDDL, Verb: "drop"},
		{Class: dbdriver.ClassAdmin, Verb: "grant"},
		{Class: dbdriver.ClassAdmin, Verb: "set"},
		{Class: dbdriver.ClassUnknown, Verb: ""},
	}

	for _, env := range envs {
		for _, grants := range grantSets {
			for _, c := range classes {
				v := gateStatement(env, c, grants)

				// Invariant: ADMIN/UNKNOWN always denied, everywhere.
				if c.Class == dbdriver.ClassAdmin || c.Class == dbdriver.ClassUnknown {
					if v.Deny != slugForbidden {
						t.Fatalf("env=%q class=%s: admin/unknown must be forbidden, got %+v", env, c.Class, v)
					}
					continue
				}
				// Invariant: reads always pass without approval.
				if c.Class == dbdriver.ClassRead {
					if v.Deny != "" || v.NeedsApproval {
						t.Fatalf("env=%q read must pass clean, got %+v", env, v)
					}
					continue
				}
				// Invariant: prod is hard read-only for every write class,
				// regardless of grants.
				if env == "prod" {
					if v.Deny != slugEnvReadonly {
						t.Fatalf("prod write must deny env-readonly, got %+v (class=%s grants=%v)", v, c.Class, grants)
					}
					continue
				}
				// Invariant: verb outside grants denied not-granted.
				key := string(c.Verb)
				if c.Class == dbdriver.ClassDDL {
					key = "ddl"
				}
				if !grants[key] {
					if v.Deny != slugNotGranted {
						t.Fatalf("ungranted %s must deny not-granted, got %+v", key, v)
					}
					continue
				}
				// Granted write: always needs approval (gate 4 default-on).
				if v.Deny != "" || !v.NeedsApproval {
					t.Fatalf("granted write must need approval, got %+v (env=%q class=%s)", v, env, c.Class)
				}
				// Invariant: DDL never auto-approvable.
				if c.Class == dbdriver.ClassDDL && v.AutoApprovable {
					t.Fatalf("DDL must not be auto-approvable")
				}
				// Invariant: unmarked env never auto-approvable (决策 2).
				if env == "" && v.AutoApprovable {
					t.Fatalf("unmarked env must not be auto-approvable")
				}
				// Invariant: missing WHERE → warning + never auto.
				if c.MissingWhere {
					if v.Warning != warnNoWhere || v.AutoApprovable {
						t.Fatalf("missing-where must warn and disable auto, got %+v", v)
					}
				}
				// DML in a marked non-prod env without the no-where flag is
				// auto-approvable.
				if c.Class == dbdriver.ClassWriteDML && !c.MissingWhere && env != "" && !v.AutoApprovable {
					t.Fatalf("marked-env DML should be auto-approvable, got %+v (env=%q)", v, env)
				}
			}
		}
	}
}

func TestApprovalBrokerResolve(t *testing.T) {
	b := newApprovalBroker()
	ch := b.create("a1")
	// Resolve BEFORE waiting — the create-then-emit order means an instant
	// Approve must never be lost.
	if err := b.resolve("a1", approvalDecision{Approved: true, Scope: scopeTaskVerb}); err != nil {
		t.Fatal(err)
	}
	d, err := b.waitOn(context.Background(), "a1", ch)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Approved || d.Scope != scopeTaskVerb {
		t.Fatalf("decision = %+v", d)
	}
	// Second resolve on same id errors.
	if err := b.resolve("a1", approvalDecision{}); err == nil {
		t.Fatal("want unknown-id error")
	}
}

func TestApprovalBrokerCancel(t *testing.T) {
	b := newApprovalBroker()
	ch := b.create("a2")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(5 * time.Millisecond); cancel() }()
	if _, err := b.waitOn(ctx, "a2", ch); err == nil {
		t.Fatal("want ctx error")
	}
	// Pending entry must be cleaned up.
	if err := b.resolve("a2", approvalDecision{}); err == nil {
		t.Fatal("entry should have been dropped")
	}
}
