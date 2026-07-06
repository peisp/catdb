package datasync

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// sliceSource is an in-memory RowSource.
type sliceSource struct {
	rows [][]any
	idx  int
	err  error // returned once idx passes errAt
}

func (s *sliceSource) Next() ([]any, error) {
	if s.err != nil && s.idx >= len(s.rows) {
		return nil, s.err
	}
	if s.idx >= len(s.rows) {
		return nil, nil
	}
	r := s.rows[s.idx]
	s.idx++
	return r, nil
}

func src(rows ...[]any) RowSource { return &sliceSource{rows: rows} }

type recorded struct {
	inserts [][]any
	updates [][]any // srcRow
	changed [][]int
	deletes [][]any
}

func recordingHandlers(r *recorded) Handlers {
	return Handlers{
		Insert: func(row []any) error { r.inserts = append(r.inserts, row); return nil },
		Update: func(s, t []any, ch []int) error {
			r.updates = append(r.updates, s)
			r.changed = append(r.changed, ch)
			return nil
		},
		Delete: func(row []any) error { r.deletes = append(r.deletes, row); return nil },
	}
}

func TestMergeClassification(t *testing.T) {
	// key = col 0. Source: 1,2,4  Target: 2(modified),3,4(same)
	source := src(
		[]any{int64(1), "a"},
		[]any{int64(2), "b"},
		[]any{int64(4), "d"},
	)
	target := src(
		[]any{int64(2), "STALE"},
		[]any{int64(3), "c"},
		[]any{int64(4), "d"},
	)
	var r recorded
	stats, err := Merge(context.Background(), source, target, []int{0}, recordingHandlers(&r))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Inserts != 1 || stats.Updates != 1 || stats.Deletes != 1 {
		t.Fatalf("stats = %+v, want 1/1/1", stats)
	}
	if stats.ScannedSource != 3 || stats.ScannedTarget != 3 {
		t.Fatalf("scanned = %d/%d, want 3/3", stats.ScannedSource, stats.ScannedTarget)
	}
	if len(r.inserts) != 1 || r.inserts[0][0] != int64(1) {
		t.Fatalf("inserts = %+v", r.inserts)
	}
	if len(r.updates) != 1 || r.updates[0][0] != int64(2) || !reflect.DeepEqual(r.changed[0], []int{1}) {
		t.Fatalf("updates = %+v changed=%v", r.updates, r.changed)
	}
	if len(r.deletes) != 1 || r.deletes[0][0] != int64(3) {
		t.Fatalf("deletes = %+v", r.deletes)
	}
}

func TestMergeEmptySides(t *testing.T) {
	// both empty
	stats, err := Merge(context.Background(), src(), src(), []int{0}, Handlers{})
	if err != nil || stats.Inserts+stats.Updates+stats.Deletes != 0 {
		t.Fatalf("empty merge: %+v err=%v", stats, err)
	}
	// source empty → all deletes
	stats, err = Merge(context.Background(), src(), src([]any{int64(1)}, []any{int64(2)}), []int{0}, Handlers{})
	if err != nil || stats.Deletes != 2 {
		t.Fatalf("want 2 deletes, got %+v err=%v", stats, err)
	}
	// target empty → all inserts
	stats, err = Merge(context.Background(), src([]any{int64(1)}), src(), []int{0}, Handlers{})
	if err != nil || stats.Inserts != 1 {
		t.Fatalf("want 1 insert, got %+v err=%v", stats, err)
	}
}

func TestMergeCompositeKey(t *testing.T) {
	source := src(
		[]any{int64(1), int64(1), "x"},
		[]any{int64(1), int64(2), "y"},
	)
	target := src(
		[]any{int64(1), int64(1), "x"},
		[]any{int64(1), int64(3), "z"},
	)
	var r recorded
	stats, err := Merge(context.Background(), source, target, []int{0, 1}, recordingHandlers(&r))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Inserts != 1 || stats.Deletes != 1 || stats.Updates != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestMergeNullAndBytes(t *testing.T) {
	// NULL orders first; []byte and string compare equal after normalization.
	source := src(
		[]any{nil, "n"},
		[]any{[]byte("k"), []byte("v")},
	)
	target := src(
		[]any{nil, "n"},
		[]any{"k", "v"},
	)
	stats, err := Merge(context.Background(), source, target, []int{0}, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Inserts+stats.Updates+stats.Deletes != 0 {
		t.Fatalf("normalized rows must be equal, got %+v", stats)
	}
}

func TestMergeMixedIntWidths(t *testing.T) {
	source := src([]any{int32(7), "a"})
	target := src([]any{int64(7), "a"})
	stats, err := Merge(context.Background(), source, target, []int{0}, Handlers{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Updates != 0 || stats.Inserts != 0 || stats.Deletes != 0 {
		t.Fatalf("int width mismatch must normalize, got %+v", stats)
	}
}

func TestMergeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Merge(ctx, src([]any{int64(1)}), src(), []int{0}, Handlers{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestMergeHandlerErrorAborts(t *testing.T) {
	boom := errors.New("boom")
	h := Handlers{Insert: func([]any) error { return boom }}
	_, err := Merge(context.Background(), src([]any{int64(1)}), src(), []int{0}, h)
	if !errors.Is(err, boom) {
		t.Fatalf("want handler error, got %v", err)
	}
}

func TestMergeSourceError(t *testing.T) {
	failing := &sliceSource{rows: [][]any{{int64(1), "a"}}, err: errors.New("read fail")}
	_, err := Merge(context.Background(), failing, src(), []int{0}, Handlers{})
	if err == nil || err.Error() != "read fail" {
		t.Fatalf("want read fail, got %v", err)
	}
}

func TestMergeEmitsEarlyProgress(t *testing.T) {
	// The first Progress call must arrive right after the initial batch pulls
	// (streams-open signal), not only after progressEvery merged rows.
	var calls []Stats
	h := Handlers{Progress: func(s Stats) { calls = append(calls, s) }}
	_, err := Merge(context.Background(),
		src([]any{int64(1), "a"}), src([]any{int64(1), "a"}), []int{0}, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) < 2 {
		t.Fatalf("want early + final progress, got %d calls", len(calls))
	}
	if calls[0].ScannedSource != 1 || calls[0].ScannedTarget != 1 {
		t.Fatalf("early progress must reflect the first rows, got %+v", calls[0])
	}
}

func TestCompareValueOrdering(t *testing.T) {
	cases := []struct {
		a, b any
		want int
	}{
		{nil, nil, 0},
		{nil, int64(1), -1},
		{int64(1), nil, 1},
		{int64(1), int64(2), -1},
		{int64(-1), uint64(1), -1},
		{uint64(2), int64(-3), 1},
		{uint64(5), uint64(5), 0},
		{1.5, int64(1), 1},
		{"a", "b", -1},
		{[]byte("x"), "x", 0},
		{false, true, -1},
		{int32(3), int64(3), 0},
	}
	for _, c := range cases {
		if got := compareValue(c.a, c.b); got != c.want {
			t.Errorf("compareValue(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPKIndexes(t *testing.T) {
	ts := &TableSync{PK: []string{"b", "a"}, Columns: []string{"a", "b", "c"}}
	idx, err := ts.pkIndexes()
	if err != nil || !reflect.DeepEqual(idx, []int{1, 0}) {
		t.Fatalf("idx=%v err=%v", idx, err)
	}
	ts.PK = []string{"missing"}
	if _, err := ts.pkIndexes(); err == nil {
		t.Fatal("missing pk column must error")
	}
}
