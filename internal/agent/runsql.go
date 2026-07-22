package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"catdb/internal/dbdriver"
	"catdb/internal/llm"
	"catdb/internal/sqlclass"
	"catdb/internal/storage"
)

// runState is the per-Send mutable state shared by the gated tools (run_sql,
// submit_plan): approval/auto-approve progress, the plan contract, and every
// handle execution needs. One per Engine.run invocation — never shared
// between turns.
type runState struct {
	sessID string
	connID string
	mode   string

	conn    dbdriver.Connection
	dialect dbdriver.Dialect
	caps    dbdriver.Capabilities
	rules   dbdriver.ScriptRules
	// classifier override probed from the driver's Dialect (may be nil).
	override dbdriver.StatementClassifier

	em *emitter
	e  *Engine

	planApproved bool
	autoVerbs    map[dbdriver.StatementVerb]bool
}

// environment reads the connection's environment label (gate 1). Read per
// call — the user may edit the connection while a session is open.
func (rs *runState) environment(ctx context.Context) string {
	p, err := rs.e.store.GetConnection(ctx, rs.connID)
	if err != nil {
		// Fail closed: unknown environment is treated as prod (hard read-only).
		return envProd
	}
	return p.Environment
}

// grants reads the session's live grant set (gate 3) — a mid-task un-check
// takes effect on the very next statement.
func (rs *runState) grants(ctx context.Context) map[string]bool {
	sess, err := rs.e.store.GetAgentSession(ctx, rs.sessID)
	if err != nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(sess.Grants))
	for _, g := range sess.Grants {
		out[strings.ToLower(g)] = true
	}
	return out
}

// buildRunSQL is the run_sql tool — every statement passes the five gates
// (§5) before touching the database. Registered in agent mode only.
func buildRunSQL(rs *runState, granted []string) Tool {
	desc := "Execute SQL on the connected database. Statements are classified and gated: " +
		"reads run directly; "
	if len(granted) > 0 {
		desc += "granted write verbs (" + strings.Join(granted, ", ") + ") require user approval per statement; "
	} else {
		desc += "this session is read-only — any write statement will be rejected; "
	}
	desc += "ADMIN statements (GRANT/SET/USE/CALL/transaction control) are always rejected. " +
		"Pass the target database explicitly; write multiple statements as separate calls when order matters."
	return Tool{
		Def: llm.ToolDef{
			Name:        "run_sql",
			Description: desc,
			InputSchema: schema(dbParam + `,"sql":{"type":"string","description":"The SQL statement to execute."}`),
		},
		ParallelOK: false, // writes are ordered; approvals are one at a time
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct{ DB, SQL string }
			if err := unmarshalArgs(args, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.SQL) == "" {
				return "", fmt.Errorf("empty sql")
			}
			return rs.execScript(ctx, a.DB, a.SQL)
		},
	}
}

// execScript splits, classifies, gates, approves and executes a run_sql call.
func (rs *runState) execScript(ctx context.Context, db, sqlText string) (string, error) {
	stmts, _ := sqlclass.ClassifyScript(sqlText, rs.rules, rs.override)
	if len(stmts) == 0 {
		return "", fmt.Errorf("no executable statement")
	}
	var results []string
	for _, st := range stmts {
		out, err := rs.execOne(ctx, db, st)
		if err != nil {
			// Stop at the first failure — later statements likely depend on it.
			// The error text (deny slug or db error) goes back to the model.
			if len(results) > 0 {
				return strings.Join(results, "\n"), fmt.Errorf("statement %d: %w", len(results)+1, err)
			}
			return "", err
		}
		results = append(results, out)
	}
	return strings.Join(results, "\n"), nil
}

func (rs *runState) execOne(ctx context.Context, db string, st sqlclass.Classified) (string, error) {
	env := rs.environment(ctx)
	v := gateStatement(env, st.C, rs.grants(ctx))

	if v.Deny != "" {
		rs.audit(ctx, st, "n/a", -1, nil, "rejected", v.Deny)
		return "", fmt.Errorf("%s: statement rejected by safety gate", v.Deny)
	}

	isWrite := st.C.Class == dbdriver.ClassWriteDML || st.C.Class == dbdriver.ClassDDL
	if isWrite && !rs.planApproved {
		// Task contract (§6): writes only after an approved plan.
		rs.audit(ctx, st, "n/a", -1, nil, "rejected", slugPlanRequired)
		return "", fmt.Errorf("%s: submit a task plan with submit_plan and get it approved before writing", slugPlanRequired)
	}

	approvalMode := "n/a"
	if v.NeedsApproval {
		if v.AutoApprovable && rs.autoVerbs[st.C.Verb] {
			approvalMode = "auto"
		} else {
			d, err := rs.requestApproval(ctx, db, st, v)
			if err != nil {
				return "", err
			}
			if !d.Approved {
				rs.audit(ctx, st, "manual", -1, nil, "rejected", d.Reason)
				reason := d.Reason
				if reason == "" {
					reason = "rejected by user"
				}
				return "", fmt.Errorf("user rejected this statement: %s", reason)
			}
			approvalMode = "manual"
			if d.Scope == scopeTaskVerb && v.AutoApprovable {
				rs.autoVerbs[st.C.Verb] = true
			}
		}
	}

	switch {
	case st.C.Class == dbdriver.ClassRead:
		return rs.execRead(ctx, db, st)
	case st.C.Class == dbdriver.ClassWriteDML && rs.caps.Transactions:
		return rs.execInTx(ctx, db, st, approvalMode)
	default:
		// DDL (implicit commit in most engines) and DML on tx-less drivers
		// execute immediately.
		return rs.execDirect(ctx, db, st, approvalMode)
	}
}

// requestApproval emits the approval card and suspends until decided (gate 4).
// The card carries a best-effort EXPLAIN estimate when the driver can plan
// write statements (§5 gate 4) — failures are silently omitted.
func (rs *runState) requestApproval(ctx context.Context, db string, st sqlclass.Classified, v gateVerdict) (approvalDecision, error) {
	id := uuid.NewString()
	ch := rs.e.broker.create(id) // register BEFORE emitting — Approve may race
	rs.em.send("agent:approval", map[string]any{
		"sessId": rs.sessID, "approvalID": id,
		"sql": st.SQL, "class": string(st.C.Class), "verb": string(st.C.Verb),
		"warning": v.Warning, "autoOffered": v.AutoApprovable,
		"explain": rs.explainEstimate(ctx, db, st.SQL),
	})
	return rs.e.broker.waitOn(ctx, id, ch)
}

// explainEstimate renders a short plan preview for the approval card.
func (rs *runState) explainEstimate(ctx context.Context, db, sql string) string {
	if !rs.caps.ExplainPlan {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q, err := dbdriver.RouteQuerier(ctx, rs.conn, db)
	if err != nil {
		return ""
	}
	set, err := q.Explain(ctx, sql)
	if err != nil {
		return ""
	}
	defer set.Close()
	out, err := renderResultSet(set, 5)
	if err != nil {
		return ""
	}
	return truncate(out, 600)
}

// execRead runs a read statement — through the open task tx when one exists
// (read-your-writes, §5 gate 5), else the pooled querier — and feeds a
// truncated view to the model while emitting the full (capped) result to the
// user path (§7).
func (rs *runState) execRead(ctx context.Context, db string, st sqlclass.Classified) (string, error) {
	ctx, cancel := rs.stmtCtx(ctx)
	defer cancel()

	var q dbdriver.Querier
	if t := rs.e.txm.get(rs.sessID); t != nil {
		q = t.tx
		t.touch(rs.e.txIdleTimeout(ctx))
	} else {
		var err error
		if q, err = dbdriver.RouteQuerier(ctx, rs.conn, db); err != nil {
			return "", err
		}
	}
	// Pre-flight EXPLAIN (§8): catches syntax errors without executing; the
	// error feeds straight back to the model for self-repair.
	if rs.caps.ExplainPlan && st.C.Verb == "select" {
		if pre, err := q.Explain(ctx, st.SQL); err != nil {
			rs.audit(ctx, st, "n/a", -1, nil, "error", err.Error())
			return "", fmt.Errorf("EXPLAIN pre-check failed: %w", err)
		} else {
			pre.Close()
		}
	}
	started := time.Now()
	resSet, err := q.Query(ctx, st.SQL)
	if err != nil {
		rs.audit(ctx, st, "n/a", -1, &started, "error", err.Error())
		return "", err
	}
	defer resSet.Close()

	modelView, rows, userRows, cols, err := rs.readBoth(ctx, resSet)
	if err != nil {
		rs.audit(ctx, st, "n/a", -1, &started, "error", err.Error())
		return "", err
	}
	rs.em.send("agent:result", map[string]any{
		"sessId": rs.sessID, "columns": cols, "rows": userRows,
		"truncated": len(userRows) >= userResultRowCap, "done": true,
	})
	rs.auditRows(ctx, st, "n/a", rows, &started, "ok", "")
	if !rs.e.settingBool(ctx, "agent.privacy.sendRowData", true) {
		// Privacy switch off (§7): the model gets shape only — row data goes
		// exclusively to the user path emitted above.
		b, jerr := json.Marshal(map[string]any{
			"columns": cols, "rowCount": rows,
			"note": "row data withheld by the user's privacy setting — it is shown to the user directly; answer from the row count and columns, or ask the user to read the values",
		})
		if jerr != nil {
			return "", jerr
		}
		return string(b), nil
	}
	return modelView, nil
}

// execInTx runs DML inside the session's task transaction, opening it (and
// its dedicated connection) on the first write. Audit entries are buffered
// in the tx and land with the commit/rollback outcome.
func (rs *runState) execInTx(ctx context.Context, db string, st sqlclass.Classified, approvalMode string) (string, error) {
	t := rs.e.txm.get(rs.sessID)
	if t == nil {
		var err error
		if t, err = rs.e.openTaskTx(ctx, rs.sessID, rs.connID); err != nil {
			return "", err
		}
	}
	ctx, cancel := rs.stmtCtx(ctx)
	defer cancel()

	started := time.Now()
	res, err := t.tx.Exec(ctx, st.SQL)
	t.touch(rs.e.txIdleTimeout(ctx))
	if err != nil {
		rs.audit(ctx, st, approvalMode, -1, &started, "error", err.Error())
		return "", err
	}
	t.record(txStmt{SQL: st.SQL, Rows: res.RowsAffected}, rs.auditEntry(st, approvalMode, res.RowsAffected, &started, "", ""))
	return fmt.Sprintf(`{"rowsAffected": %d, "note": "executed inside the task transaction; the user commits or rolls back at the end"}`, res.RowsAffected), nil
}

// execDirect executes immediately (DDL / tx-less drivers) and audits at once.
func (rs *runState) execDirect(ctx context.Context, db string, st sqlclass.Classified, approvalMode string) (string, error) {
	ctx, cancel := rs.stmtCtx(ctx)
	defer cancel()
	q, err := dbdriver.RouteQuerier(ctx, rs.conn, db)
	if err != nil {
		return "", err
	}
	started := time.Now()
	res, err := q.Exec(ctx, st.SQL)
	if err != nil {
		rs.audit(ctx, st, approvalMode, -1, &started, "error", err.Error())
		return "", err
	}
	rs.auditRows(ctx, st, approvalMode, res.RowsAffected, &started, "ok", "")
	note := ""
	if st.C.Class == dbdriver.ClassWriteDML {
		note = `, "note": "this driver has no transactions — the statement is already committed"`
	}
	return fmt.Sprintf(`{"rowsAffected": %d%s}`, res.RowsAffected, note), nil
}

const userResultRowCap = 500 // compact in-chat table cap (full grid view is M3, §18.3)

// readBoth drains a result set once, serving both paths of §7: the
// model-facing truncated view (llmResultRows / 256-char cells / 32KB) and the
// user-facing rows (capped at userResultRowCap).
func (rs *runState) readBoth(ctx context.Context, set dbdriver.ResultSet) (modelView string, total int64, userRows [][]any, colNames []string, err error) {
	cols := set.Columns()
	colNames = make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = c.Name
	}
	modelCap := rs.llmResultRows(ctx)
	for len(userRows) < userResultRowCap {
		batch, done, berr := set.Next(userResultRowCap - len(userRows))
		if berr != nil {
			return "", 0, nil, nil, berr
		}
		userRows = append(userRows, batch...)
		if done {
			break
		}
	}
	total = int64(len(userRows))
	modelRows := userRows
	truncated := false
	if len(modelRows) > modelCap {
		modelRows = modelRows[:modelCap]
		truncated = true
	}
	view := map[string]any{"columns": colNames, "rows": clampCells(modelRows)}
	if truncated || len(userRows) >= userResultRowCap {
		view["truncated"] = true
		view["note"] = fmt.Sprintf("data is incomplete: only the first %d rows are shown to you; the user sees up to %d", len(modelRows), userResultRowCap)
	}
	b, jerr := json.Marshal(view)
	if jerr != nil {
		return "", 0, nil, nil, jerr
	}
	if len(b) > 32*1024 {
		b = append(b[:32*1024], []byte("…(truncated)")...)
	}
	return string(b), total, userRows, colNames, nil
}

func clampCells(rows [][]any) [][]any {
	for _, r := range rows {
		for i, v := range r {
			if s, ok := v.(string); ok && len(s) > maxCellChars {
				r[i] = s[:maxCellChars] + "…"
			}
		}
	}
	return rows
}

// --- submit_plan (task contract, §6) -----------------------------------------

func buildSubmitPlan(rs *runState) Tool {
	return Tool{
		Def: llm.ToolDef{
			Name:        "submit_plan",
			Description: "Submit the task plan for user approval BEFORE any write statement. Required for every task that modifies data or schema. Include the goal, the exact statements you intend to run, and the estimated impact.",
			InputSchema: schema(`"goal":{"type":"string"},"statements":{"type":"array","items":{"type":"string"}},"impact":{"type":"string","description":"Estimated affected rows/objects."}`),
		},
		ParallelOK: false,
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Goal       string
				Statements []string
				Impact     string
			}
			if err := unmarshalArgs(args, &a); err != nil {
				return "", err
			}
			if a.Goal == "" || len(a.Statements) == 0 {
				return "", fmt.Errorf("plan needs a goal and at least one statement")
			}
			id := uuid.NewString()
			ch := rs.e.broker.create(id) // register BEFORE emitting
			rs.em.send("agent:plan", map[string]any{
				"sessId": rs.sessID, "planID": id,
				"goal": a.Goal, "statements": a.Statements, "impact": a.Impact,
			})
			d, err := rs.e.broker.waitOn(ctx, id, ch)
			if err != nil {
				return "", err
			}
			if !d.Approved {
				reason := d.Reason
				if reason == "" {
					reason = "plan rejected by user"
				}
				return "", fmt.Errorf("plan rejected: %s — revise the plan or ask the user", reason)
			}
			rs.planApproved = true
			return "plan approved by the user — you may now execute the planned statements with run_sql; report any deviation from the plan when you finish", nil
		},
	}
}

// --- audit helpers -----------------------------------------------------------

func (rs *runState) auditEntry(st sqlclass.Classified, approval string, rows int64, started *time.Time, status, errText string) storage.AgentAuditEntry {
	e := storage.AgentAuditEntry{
		SessionID: rs.sessID,
		ConnID:    rs.connID,
		SQL:       st.SQL,
		Class:     auditClass(st.C),
		Approval:  approval,
		Status:    status,
		Error:     errText,
	}
	if rows >= 0 {
		r := rows
		e.Rows = &r
	}
	if started != nil {
		d := time.Since(*started).Milliseconds()
		e.DurationMS = &d
	}
	return e
}

func (rs *runState) audit(ctx context.Context, st sqlclass.Classified, approval string, rows int64, started *time.Time, status, errText string) {
	_, _ = rs.e.store.AppendAgentAudit(ctx, rs.auditEntry(st, approval, rows, started, status, errText))
}

func (rs *runState) auditRows(ctx context.Context, st sqlclass.Classified, approval string, rows int64, started *time.Time, status, errText string) {
	rs.audit(ctx, st, approval, rows, started, status, errText)
}

// auditClass records the verb for write classes (grants/approval match on the
// verb, §11), the coarse class otherwise.
func auditClass(c dbdriver.StatementClassification) string {
	if c.Class == dbdriver.ClassWriteDML || c.Class == dbdriver.ClassDDL {
		if c.Verb != "" {
			return string(c.Verb)
		}
	}
	return string(c.Class)
}

// stmtCtx derives the per-statement timeout ctx (agent.limits.stmtTimeoutSec,
// default 60s).
func (rs *runState) stmtCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	sec := 60
	if v := rs.e.setting(ctx, "agent.limits.stmtTimeoutSec"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sec = n
		}
	}
	return context.WithTimeout(ctx, time.Duration(sec)*time.Second)
}

// llmResultRows reads agent.limits.llmResultRows (default 50).
func (rs *runState) llmResultRows(ctx context.Context) int {
	if v := rs.e.setting(ctx, "agent.limits.llmResultRows"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 50
}
