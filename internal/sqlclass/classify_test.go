package sqlclass

import (
	"testing"

	"catdb/internal/dbdriver"
)

func TestClassifyCorpus(t *testing.T) {
	cases, err := Corpus()
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	if len(cases) < 60 {
		t.Fatalf("corpus must have >= 60 cases, got %d", len(cases))
	}
	for _, cc := range cases {
		cc := cc
		t.Run(cc.SQL, func(t *testing.T) {
			got := ClassifyStatement(cc.SQL)
			if got.Class != dbdriver.StatementClass(cc.Class) {
				t.Errorf("class: got %q want %q", got.Class, cc.Class)
			}
			if got.Verb != dbdriver.StatementVerb(cc.Verb) {
				t.Errorf("verb: got %q want %q", got.Verb, cc.Verb)
			}
			if cc.MissingWhere != nil && got.MissingWhere != *cc.MissingWhere {
				t.Errorf("missingWhere: got %v want %v", got.MissingWhere, *cc.MissingWhere)
			}
		})
	}
}

func TestRiskier(t *testing.T) {
	tests := []struct {
		a, b, want dbdriver.StatementClass
	}{
		{dbdriver.ClassRead, dbdriver.ClassWriteDML, dbdriver.ClassWriteDML},
		{dbdriver.ClassWriteDML, dbdriver.ClassRead, dbdriver.ClassWriteDML},
		{dbdriver.ClassDDL, dbdriver.ClassAdmin, dbdriver.ClassAdmin},
		{dbdriver.ClassAdmin, dbdriver.ClassUnknown, dbdriver.ClassUnknown},
		{dbdriver.ClassUnknown, dbdriver.ClassAdmin, dbdriver.ClassUnknown},
		{dbdriver.ClassRead, dbdriver.ClassRead, dbdriver.ClassRead},
	}
	for _, tt := range tests {
		if got := Riskier(tt.a, tt.b); got != tt.want {
			t.Errorf("Riskier(%q,%q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

// mysqlRules mirrors the MySQL script-splitting rules (backticks, '#' comments,
// backslash escapes, DELIMITER).
var mysqlRules = dbdriver.ScriptRules{
	BacktickIdentifiers: true,
	BackslashEscapes:    true,
	HashComments:        true,
	ClientDelimiter:     true,
}

func TestClassifyScriptSplitAndBatch(t *testing.T) {
	// A read followed by the only write (an UPDATE without WHERE): the batch is
	// that write and carries its Verb + MissingWhere unambiguously.
	script := "SELECT 1; UPDATE t SET a = 1"
	stmts, batch := ClassifyScript(script, mysqlRules, nil)
	if len(stmts) != 2 {
		t.Fatalf("want 2 statements, got %d", len(stmts))
	}
	if stmts[0].C.Class != dbdriver.ClassRead {
		t.Errorf("stmt 0 class = %q, want read", stmts[0].C.Class)
	}
	if stmts[1].C.Class != dbdriver.ClassWriteDML {
		t.Errorf("stmt 1 class = %q, want write_dml", stmts[1].C.Class)
	}
	if batch.Class != dbdriver.ClassWriteDML || batch.Verb != "update" || !batch.MissingWhere {
		t.Errorf("batch = %+v, want write_dml/update missingWhere=true", batch)
	}
}

func TestClassifyScriptBatchTieKeepsFirst(t *testing.T) {
	// Two writes of the same risk: the batch deterministically keeps the first.
	_, batch := ClassifyScript("INSERT INTO t VALUES (1); DELETE FROM t", mysqlRules, nil)
	if batch.Class != dbdriver.ClassWriteDML || batch.Verb != "insert" {
		t.Errorf("batch = %+v, want write_dml/insert (first of equal risk)", batch)
	}
}

func TestClassifyScriptBatchTakesHighestRisk(t *testing.T) {
	// A read followed by a DROP: the whole batch must be DDL.
	stmts, batch := ClassifyScript("SELECT 1; DROP TABLE t", mysqlRules, nil)
	if len(stmts) != 2 {
		t.Fatalf("want 2 statements, got %d", len(stmts))
	}
	if batch.Class != dbdriver.ClassDDL || batch.Verb != "drop" {
		t.Errorf("batch = %+v, want ddl/drop", batch)
	}
}

func TestClassifyScriptEmpty(t *testing.T) {
	// Comment-only script yields no statements and a READ baseline batch.
	stmts, batch := ClassifyScript("-- just a comment\n/* nothing here */", mysqlRules, nil)
	if len(stmts) != 0 {
		t.Fatalf("want 0 statements, got %d", len(stmts))
	}
	if batch.Class != dbdriver.ClassRead {
		t.Errorf("empty batch class = %q, want read", batch.Class)
	}
}

// fakeClassifier overrides one specific statement and hands everything else back.
type fakeClassifier struct {
	target  string
	verdict dbdriver.StatementClassification
}

func (f fakeClassifier) ClassifyStatement(sql string) dbdriver.StatementClassification {
	if sql == f.target {
		return f.verdict
	}
	return dbdriver.StatementClassification{Class: dbdriver.ClassUnknown}
}

func TestClassifyScriptOverridePriority(t *testing.T) {
	// Generic classifier would call "SELECT do_write()" a plain read; a driver
	// override promotes it to write_dml, and that must be honored.
	target := "SELECT do_write()"
	ov := fakeClassifier{
		target:  target,
		verdict: dbdriver.StatementClassification{Class: dbdriver.ClassWriteDML, Verb: "select"},
	}
	stmts, batch := ClassifyScript(target, mysqlRules, ov)
	if len(stmts) != 1 {
		t.Fatalf("want 1 statement, got %d", len(stmts))
	}
	if stmts[0].C.Class != dbdriver.ClassWriteDML {
		t.Errorf("override not applied: got %q", stmts[0].C.Class)
	}
	if batch.Class != dbdriver.ClassWriteDML {
		t.Errorf("batch = %q, want write_dml", batch.Class)
	}
}

func TestClassifyScriptOverrideFallsBackWhenUnknown(t *testing.T) {
	// Override returns unknown for a statement it does not handle → generic wins.
	ov := fakeClassifier{target: "NOTHING", verdict: dbdriver.StatementClassification{Class: dbdriver.ClassDDL}}
	stmts, _ := ClassifyScript("SELECT 1", mysqlRules, ov)
	if stmts[0].C.Class != dbdriver.ClassRead {
		t.Errorf("fallback failed: got %q, want read", stmts[0].C.Class)
	}
}
