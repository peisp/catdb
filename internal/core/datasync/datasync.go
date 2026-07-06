// Package datasync implements primary-key ordered streaming merge comparison
// between two tables — the core of the Data Synchronization feature.
//
// Both sides are read with `SELECT <cols> ... ORDER BY <pk>` through the
// streaming ResultSet interface, so memory stays bounded regardless of table
// size. A two-pointer merge classifies every row as insert (source-only),
// update (both sides, non-key values differ), or delete (target-only), and
// hands it to caller-supplied handlers: counters + samples in Compare mode,
// parameterized Editor statements inside target-side transactions in Execute
// mode.
//
// Known limitation (MVP): key ordering uses Go-side comparison after
// normalization. String primary keys under case-insensitive collations may
// order differently than the server's ORDER BY, which can misalign the merge.
// Numeric and binary-collated keys — the overwhelmingly common case — are
// exact.
package datasync

import (
	"context"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// Stats accumulates one table's merge outcome.
type Stats struct {
	Inserts       int64 `json:"inserts"`
	Updates       int64 `json:"updates"`
	Deletes       int64 `json:"deletes"`
	ScannedSource int64 `json:"scannedSource"`
	ScannedTarget int64 `json:"scannedTarget"`
}

// DiffSample is one retained difference for the preview UI. Key holds the
// primary-key values; Columns names the changed columns (updates only).
type DiffSample struct {
	Kind    string   `json:"kind"` // "insert" | "update" | "delete"
	Key     []any    `json:"key"`
	Columns []string `json:"columns,omitempty"`
}

// RowSource is a pull-based row stream. Next returns nil when exhausted.
type RowSource interface {
	Next() ([]any, error)
}

// resultSetSource adapts a dbdriver.ResultSet to RowSource with batch pulls.
type resultSetSource struct {
	rs    dbdriver.ResultSet
	batch int
	buf   [][]any
	idx   int
	done  bool
}

// NewResultSetSource wraps rs into a RowSource pulling `batch` rows at a time.
func NewResultSetSource(rs dbdriver.ResultSet, batch int) RowSource {
	if batch <= 0 {
		batch = 500
	}
	return &resultSetSource{rs: rs, batch: batch}
}

func (s *resultSetSource) Next() ([]any, error) {
	for s.idx >= len(s.buf) {
		if s.done {
			return nil, nil
		}
		rows, done, err := s.rs.Next(s.batch)
		if err != nil {
			return nil, err
		}
		s.buf, s.idx, s.done = rows, 0, done
	}
	row := s.buf[s.idx]
	s.idx++
	return row, nil
}

// Handlers receives each classified difference during a merge. A nil handler
// skips that class (Compare mode sets all three to counters; Execute wires
// them to the write path). Returning an error aborts the merge.
type Handlers struct {
	Insert func(srcRow []any) error
	Update func(srcRow, tgtRow []any, changed []int) error
	Delete func(tgtRow []any) error
	// Progress is invoked periodically (about once per batch) with running
	// stats; may be nil.
	Progress func(Stats)
}

// Merge runs the ordered two-pointer comparison. pkIdx are the primary-key
// column positions (same positions on both sides — the caller selects the
// same column list in the same order). Rows must arrive ordered by that key
// ascending on both sides.
func Merge(ctx context.Context, src, tgt RowSource, pkIdx []int, h Handlers) (Stats, error) {
	var stats Stats
	if len(pkIdx) == 0 {
		return stats, fmt.Errorf("datasync: primary key required")
	}

	const progressEvery = 500
	sinceProgress := 0
	tick := func() {
		sinceProgress++
		if h.Progress != nil && sinceProgress >= progressEvery {
			sinceProgress = 0
			h.Progress(stats)
		}
	}

	srcRow, err := src.Next()
	if err != nil {
		return stats, err
	}
	if srcRow != nil {
		stats.ScannedSource++
	}
	tgtRow, err := tgt.Next()
	if err != nil {
		return stats, err
	}
	if tgtRow != nil {
		stats.ScannedTarget++
	}

	advanceSrc := func() error {
		srcRow, err = src.Next()
		if err != nil {
			return err
		}
		if srcRow != nil {
			stats.ScannedSource++
		}
		return nil
	}
	advanceTgt := func() error {
		tgtRow, err = tgt.Next()
		if err != nil {
			return err
		}
		if tgtRow != nil {
			stats.ScannedTarget++
		}
		return nil
	}

	for srcRow != nil || tgtRow != nil {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		var cmp int
		switch {
		case srcRow == nil:
			cmp = 1 // only target rows left → deletes
		case tgtRow == nil:
			cmp = -1 // only source rows left → inserts
		default:
			cmp = compareKeys(srcRow, tgtRow, pkIdx)
		}
		switch {
		case cmp < 0:
			stats.Inserts++
			if h.Insert != nil {
				if err := h.Insert(srcRow); err != nil {
					return stats, err
				}
			}
			if err := advanceSrc(); err != nil {
				return stats, err
			}
		case cmp > 0:
			stats.Deletes++
			if h.Delete != nil {
				if err := h.Delete(tgtRow); err != nil {
					return stats, err
				}
			}
			if err := advanceTgt(); err != nil {
				return stats, err
			}
		default:
			if changed := changedColumns(srcRow, tgtRow, pkIdx); len(changed) > 0 {
				stats.Updates++
				if h.Update != nil {
					if err := h.Update(srcRow, tgtRow, changed); err != nil {
						return stats, err
					}
				}
			}
			if err := advanceSrc(); err != nil {
				return stats, err
			}
			if err := advanceTgt(); err != nil {
				return stats, err
			}
		}
		tick()
	}
	if h.Progress != nil {
		h.Progress(stats)
	}
	return stats, nil
}

// changedColumns lists the non-key column positions whose values differ.
func changedColumns(a, b []any, pkIdx []int) []int {
	key := map[int]bool{}
	for _, i := range pkIdx {
		key[i] = true
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var out []int
	for i := 0; i < n; i++ {
		if key[i] {
			continue
		}
		if !valuesEqual(a[i], b[i]) {
			out = append(out, i)
		}
	}
	return out
}

// compareKeys orders two rows by their key columns.
func compareKeys(a, b []any, pkIdx []int) int {
	for _, i := range pkIdx {
		if c := compareValue(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// ---- value normalization / comparison ---------------------------------------
//
// The scanner yields int64/uint64/float64/bool/string/[]byte/nil. The two
// sides run the same driver in MVP, so same column type → same Go type; the
// normalization below still guards against []byte-vs-string and integer-width
// mismatches.

func normalize(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint:
		return uint64(x)
	case uint8:
		return uint64(x)
	case uint16:
		return uint64(x)
	case uint32:
		return uint64(x)
	case float32:
		return float64(x)
	default:
		return v
	}
}

func valuesEqual(a, b any) bool {
	return compareValue(a, b) == 0
}

// compareValue orders two normalized scalars: nil first, then numerics,
// bools, strings. Cross-kind falls back to string form so the merge stays
// total (never panics on odd driver output).
func compareValue(a, b any) int {
	av, bv := normalize(a), normalize(b)
	if av == nil || bv == nil {
		switch {
		case av == nil && bv == nil:
			return 0
		case av == nil:
			return -1
		default:
			return 1
		}
	}
	switch x := av.(type) {
	case int64:
		switch y := bv.(type) {
		case int64:
			return cmpOrdered(x, y)
		case uint64:
			if x < 0 {
				return -1
			}
			return cmpOrdered(uint64(x), y)
		case float64:
			return cmpOrdered(float64(x), y)
		}
	case uint64:
		switch y := bv.(type) {
		case uint64:
			return cmpOrdered(x, y)
		case int64:
			if y < 0 {
				return 1
			}
			return cmpOrdered(x, uint64(y))
		case float64:
			return cmpOrdered(float64(x), y)
		}
	case float64:
		switch y := bv.(type) {
		case float64:
			return cmpOrdered(x, y)
		case int64:
			return cmpOrdered(x, float64(y))
		case uint64:
			return cmpOrdered(x, float64(y))
		}
	case bool:
		if y, ok := bv.(bool); ok {
			switch {
			case x == y:
				return 0
			case !x:
				return -1
			default:
				return 1
			}
		}
	case string:
		if y, ok := bv.(string); ok {
			return strings.Compare(x, y)
		}
	}
	return strings.Compare(fmt.Sprint(av), fmt.Sprint(bv))
}

func cmpOrdered[T int64 | uint64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
