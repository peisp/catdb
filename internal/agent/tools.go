package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
	"catdb/internal/llm"
)

// Limits applied to tool results fed back to the model (AGENT_DESIGN.md §4.2,
// §7). User-facing full results take a separate path and are not affected.
const (
	maxListItems  = 200 // metadata lists are truncated beyond this
	maxSampleRows = 20  // table_sample row cap
	maxCellChars  = 256 // per-cell truncation for row data
)

// Tool is one callable exposed to the model. Run returns the string fed back
// as the tool result; a non-nil error becomes an is_error tool result (the
// model can react to it).
type Tool struct {
	Def        llm.ToolDef
	ParallelOK bool
	Run        func(ctx context.Context, args json.RawMessage) (string, error)
}

// toolEnv is everything tool implementations need for one session.
type toolEnv struct {
	conn    dbdriver.Connection
	dialect dbdriver.Dialect
	caps    dbdriver.Capabilities
	privacy bool // sendRowData allowed
}

func schema(props string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"additionalProperties":false}`)
}

const dbParam = `"db":{"type":"string","description":"Target database name. Always pass it explicitly."}`
const schemaParam = `"schema":{"type":"string","description":"Target schema name."}`
const tableParam = `"table":{"type":"string","description":"Table name."}`

// buildTools assembles the tool set for a session, trimmed by driver
// capabilities and the privacy switch (AGENT_DESIGN.md §4.2). Mode "ask"
// gets the read/metadata set only; run_sql is added in the agent-mode
// milestone (M2) behind the safety gates.
func buildTools(env toolEnv) []Tool {
	meta := env.conn.Metadata()
	tools := []Tool{
		{
			Def: llm.ToolDef{
				Name:        "list_databases",
				Description: "List all databases on the connected server.",
				InputSchema: schema(``),
			},
			ParallelOK: true,
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				names, err := meta.ListDatabases(ctx)
				if err != nil {
					return "", err
				}
				return marshalList(names)
			},
		},
		{
			Def: llm.ToolDef{
				Name:        "list_tables",
				Description: "List tables in a database" + ifStr(env.caps.Schemas, " and schema", "") + ", with comments.",
				InputSchema: schema(dbParam + ifStr(env.caps.Schemas, ","+schemaParam, "")),
			},
			ParallelOK: true,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ DB, Schema string }
				if err := unmarshalArgs(args, &a); err != nil {
					return "", err
				}
				ts, err := meta.ListTables(ctx, a.DB, a.Schema)
				if err != nil {
					return "", err
				}
				type row struct {
					Name    string `json:"name"`
					Comment string `json:"comment,omitempty"`
				}
				out := make([]row, 0, len(ts))
				for _, t := range ts {
					out = append(out, row{t.Name, t.Comment})
				}
				return marshalList(out)
			},
		},
		{
			Def: llm.ToolDef{
				Name:        "get_table_schema",
				Description: "Get the full structure of a table: columns, indexes and foreign keys, in one call. Use this before referencing any column.",
				InputSchema: schema(dbParam + "," + schemaParam + "," + tableParam),
			},
			ParallelOK: true,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ DB, Schema, Table string }
				if err := unmarshalArgs(args, &a); err != nil {
					return "", err
				}
				cols, err := meta.ListColumns(ctx, a.DB, a.Schema, a.Table)
				if err != nil {
					return "", err
				}
				idx, err := meta.ListIndexes(ctx, a.DB, a.Schema, a.Table)
				if err != nil {
					return "", err
				}
				fks, err := meta.ListForeignKeys(ctx, a.DB, a.Schema, a.Table)
				if err != nil {
					return "", err
				}
				b, err := json.Marshal(map[string]any{"columns": cols, "indexes": idx, "foreignKeys": fks})
				if err != nil {
					return "", err
				}
				return string(b), nil
			},
		},
		{
			Def: llm.ToolDef{
				Name:        "get_table_ddl",
				Description: "Get the native CREATE TABLE statement of a table.",
				InputSchema: schema(dbParam + "," + schemaParam + "," + tableParam),
			},
			ParallelOK: true,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ DB, Schema, Table string }
				if err := unmarshalArgs(args, &a); err != nil {
					return "", err
				}
				return meta.GetCreateTable(ctx, a.DB, a.Schema, a.Table)
			},
		},
	}

	if env.caps.Views {
		tools = append(tools, Tool{
			Def: llm.ToolDef{
				Name:        "list_views",
				Description: "List views in a database.",
				InputSchema: schema(dbParam + ifStr(env.caps.Schemas, ","+schemaParam, "")),
			},
			ParallelOK: true,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ DB, Schema string }
				if err := unmarshalArgs(args, &a); err != nil {
					return "", err
				}
				vs, err := meta.ListViews(ctx, a.DB, a.Schema)
				if err != nil {
					return "", err
				}
				names := make([]string, 0, len(vs))
				for _, v := range vs {
					names = append(names, v.Name)
				}
				return marshalList(names)
			},
		})
	}

	if env.privacy {
		tools = append(tools, Tool{
			Def: llm.ToolDef{
				Name:        "table_sample",
				Description: fmt.Sprintf("Fetch up to %d sample rows from a table to understand its data shape.", maxSampleRows),
				InputSchema: schema(dbParam + "," + schemaParam + "," + tableParam),
			},
			ParallelOK: true,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ DB, Schema, Table string }
				if err := unmarshalArgs(args, &a); err != nil {
					return "", err
				}
				q, err := dbdriver.RouteQuerier(ctx, env.conn, a.DB)
				if err != nil {
					return "", err
				}
				base := "SELECT * FROM " + dbdriver.QualifyTable(env.dialect, a.DB, a.Schema, a.Table)
				rs, err := q.Query(ctx, env.dialect.Paginate(base, maxSampleRows, 0))
				if err != nil {
					return "", err
				}
				defer rs.Close()
				return renderResultSet(rs, maxSampleRows)
			},
		})
	}

	if env.caps.ExplainPlan {
		tools = append(tools, Tool{
			Def: llm.ToolDef{
				Name:        "explain",
				Description: "Get the execution plan of a read-only query (SELECT/WITH only) without running it.",
				InputSchema: schema(dbParam + `,` + schemaParam + `,"sql":{"type":"string","description":"The SELECT statement to explain."}`),
			},
			ParallelOK: true,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var a struct{ DB, Schema, SQL string }
				if err := unmarshalArgs(args, &a); err != nil {
					return "", err
				}
				// Read-only guard: EXPLAIN ANALYZE variants really execute the
				// statement in some engines, so never pass a write through.
				// (Prefix allowlist for M1; replaced by the statement
				// classifier in the safety milestone — §5 gate 2.)
				if !readOnlyPrefix(a.SQL) {
					return "", fmt.Errorf("only SELECT/WITH statements can be explained")
				}
				q, release, err := dbdriver.NamespacedQuerier(ctx, env.conn, env.dialect, env.caps, a.DB, a.Schema)
				if err != nil {
					return "", err
				}
				defer release()
				rs, err := q.Explain(ctx, a.SQL)
				if err != nil {
					return "", err
				}
				defer rs.Close()
				return renderResultSet(rs, maxListItems)
			},
		})
	}

	return tools
}

// readOnlyPrefix reports whether sql's first keyword is a read-only verb.
// Deliberately strict: anything unrecognized is rejected.
func readOnlyPrefix(sql string) bool {
	s := strings.TrimSpace(sql)
	for strings.HasPrefix(s, "--") || strings.HasPrefix(s, "/*") {
		if strings.HasPrefix(s, "--") {
			i := strings.IndexByte(s, '\n')
			if i < 0 {
				return false
			}
			s = strings.TrimSpace(s[i+1:])
			continue
		}
		i := strings.Index(s, "*/")
		if i < 0 {
			return false
		}
		s = strings.TrimSpace(s[i+2:])
	}
	word := s
	if i := strings.IndexAny(s, " \t\r\n("); i >= 0 {
		word = s[:i]
	}
	switch strings.ToUpper(word) {
	case "SELECT", "WITH", "TABLE":
		return true
	}
	return false
}

// renderResultSet reads up to limit rows and renders a compact JSON view for
// the model: column names once, rows as arrays, cells truncated, and an
// explicit truncation notice (§7 — the model must know data is incomplete).
func renderResultSet(rs dbdriver.ResultSet, limit int) (string, error) {
	cols := rs.Columns()
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	var rows [][]any
	truncated := false
	for len(rows) < limit {
		batch, done, err := rs.Next(limit - len(rows))
		if err != nil {
			return "", err
		}
		rows = append(rows, batch...)
		if done {
			break
		}
	}
	// Probe one more batch to learn whether we stopped short.
	if len(rows) == limit {
		if extra, done, err := rs.Next(1); err == nil && (len(extra) > 0 || !done) {
			truncated = truncated || len(extra) > 0
		}
	}
	for _, r := range rows {
		for i, v := range r {
			if s, ok := v.(string); ok && len(s) > maxCellChars {
				r[i] = s[:maxCellChars] + "…"
			}
		}
	}
	out := map[string]any{"columns": names, "rows": rows}
	if truncated {
		out["truncated"] = true
		out["note"] = fmt.Sprintf("only the first %d rows are shown; the data is incomplete", limit)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// marshalList marshals a slice, truncating past maxListItems with an explicit
// notice (§4.2).
func marshalList[T any](items []T) (string, error) {
	truncated := false
	if len(items) > maxListItems {
		items = items[:maxListItems]
		truncated = true
	}
	var out any = items
	if truncated {
		out = map[string]any{
			"items":     items,
			"truncated": true,
			"note":      fmt.Sprintf("only the first %d items are shown", maxListItems),
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalArgs(args json.RawMessage, into any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, into); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func ifStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
