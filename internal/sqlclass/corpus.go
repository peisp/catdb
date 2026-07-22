package sqlclass

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed testdata/corpus.json
var corpusJSON []byte

// CorpusCase is one entry in the shared classifier corpus (testdata/corpus.json).
// The same corpus drives both this package's unit tests and the driver contract
// suite, so a newly discovered bypass only needs a new corpus line to regress
// against every driver.
type CorpusCase struct {
	SQL   string `json:"sql"`
	Class string `json:"class"`
	Verb  string `json:"verb"`
	// MissingWhere is a pointer so absent means "don't assert" (only meaningful
	// for UPDATE/DELETE cases).
	MissingWhere *bool `json:"missingWhere,omitempty"`
}

// Corpus loads the embedded classifier corpus.
func Corpus() ([]CorpusCase, error) {
	var cs []CorpusCase
	if err := json.Unmarshal(corpusJSON, &cs); err != nil {
		return nil, fmt.Errorf("sqlclass: parse corpus: %w", err)
	}
	return cs, nil
}
