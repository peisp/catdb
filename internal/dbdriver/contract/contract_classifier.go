package contract

import (
	"testing"

	"catdb/internal/dbdriver"
	"catdb/internal/sqlclass"
)

// RunClassifierCorpus runs the shared statement-classifier corpus
// (internal/sqlclass/testdata/corpus.json) through a driver's optional
// StatementClassifier extension, using the same override-first-then-generic
// pipeline the safety gate uses at runtime: the driver's ClassifyStatement is
// consulted first, and any ClassUnknown result hands the statement back to the
// generic lexical classifier.
//
// A driver plugin that implements dbdriver.StatementClassifier calls this from
// its contract test so dialect-specific overrides are regression-checked
// against the same corpus every driver shares — a newly discovered bypass is
// fixed once (a new corpus line) and re-verified everywhere.
//
// classifier may be nil: the corpus is then validated against the pure generic
// classifier (which is exactly what the corpus encodes), so this helper always
// has something meaningful to assert even before any driver adopts the
// extension.
func RunClassifierCorpus(t *testing.T, classifier dbdriver.StatementClassifier) {
	t.Helper()
	cases, err := sqlclass.Corpus()
	if err != nil {
		t.Fatalf("load classifier corpus: %v", err)
	}
	for _, cc := range cases {
		cc := cc
		t.Run(cc.SQL, func(t *testing.T) {
			got := classifyWith(cc.SQL, classifier)
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

// classifyWith mirrors the runtime gate-2 pipeline for a single statement.
func classifyWith(sql string, classifier dbdriver.StatementClassifier) dbdriver.StatementClassification {
	if classifier != nil {
		if c := classifier.ClassifyStatement(sql); c.Class != dbdriver.ClassUnknown {
			return c
		}
	}
	return sqlclass.ClassifyStatement(sql)
}
