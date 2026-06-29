// Package sqlscript splits a SQL script into individual statements the way a
// SQL client (e.g. the mysql CLI) does — respecting quoted strings and
// comments, and honoring the client-side DELIMITER directive.
//
// DELIMITER is NOT a statement the server understands; it is a client
// convention that lets a routine body (whose statements end in `;`) be sent as
// a single statement. The server only ever sees the statements between
// delimiters, never the DELIMITER lines themselves.
//
// The rules implemented here are MySQL/ANSI flavored (the MVP target). A future
// driver with different quoting (e.g. Postgres dollar-quoting) should layer its
// own splitter behind the dialect rather than extend this one.
package sqlscript

import (
	"bufio"
	"io"
	"strings"
)

const defaultDelimiter = ";"

// maxLineBytes caps a single physical line when streaming. SQL dumps routinely
// put a whole multi-row INSERT on one line, so this is generous.
const maxLineBytes = 16 * 1024 * 1024

// Split breaks an in-memory script into trimmed, non-empty statements. It is a
// thin wrapper over the same state machine SplitStream uses, so both honor
// identical quoting / comment / DELIMITER rules. Comment-only and
// whitespace-only spans (and the DELIMITER directives themselves) never produce
// a statement; returned statements no longer contain their trailing delimiter.
func Split(script string) []string {
	var out []string
	sp := newSplitter(func(s string) error {
		out = append(out, s)
		return nil
	})
	sp.feed(script)
	sp.finish()
	return out
}

// SplitStream splits SQL read from r, invoking fn for each statement as it is
// found. It holds at most one statement (plus one physical line) in memory at a
// time, so it is safe for arbitrarily large dump files. If fn returns an error,
// splitting stops and that error is returned.
func SplitStream(r io.Reader, fn func(stmt string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	sp := newSplitter(fn)
	for sc.Scan() {
		// Scanner strips the line terminator; re-add '\n' so multi-line
		// strings/comments keep their content and the statement reads back
		// faithfully.
		if sp.feed(sc.Text() + "\n"); sp.err != nil {
			return sp.err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	sp.finish()
	return sp.err
}

// scanState is the cross-feed tokenizer state — what (if any) multi-line
// construct is currently open.
type scanState int

const (
	stNormal scanState = iota
	stSingle
	stDouble
	stBacktick
	stBlock
)

type splitter struct {
	delim      string
	state      scanState
	buf        strings.Builder
	hasContent bool // current statement has a real (non-ws, non-comment) char
	emit       func(string) error
	err        error // first error returned by emit; halts further work
}

func newSplitter(emit func(string) error) *splitter {
	return &splitter{delim: defaultDelimiter, emit: emit}
}

// feed processes one chunk of input. State persists across calls so a string or
// block comment may span chunks. Stops early (no-op) once err is set.
func (sp *splitter) feed(s string) {
	if sp.err != nil {
		return
	}
	n := len(s)
	i := 0

	// Resume a multi-line string / block comment opened in a previous feed.
	switch sp.state {
	case stBlock:
		i = sp.consumeBlock(s, 0)
	case stSingle, stDouble, stBacktick:
		i = sp.consumeString(s, 0)
	}
	if sp.state != stNormal {
		return // the open construct ran to the end of this chunk
	}

	for i < n && sp.err == nil {
		c := s[i]

		// DELIMITER directive — only at statement start; occupies a whole line.
		if !sp.hasContent && (c == 'd' || c == 'D') {
			if nd, next, ok := tryDelimiter(s, i); ok {
				sp.delim = nd
				sp.buf.Reset()
				sp.hasContent = false
				i = next
				continue
			}
		}

		// Comments — kept in the buffer (so adjacent tokens aren't merged) but
		// they never set hasContent, so a pure-comment span is dropped.
		if c == '-' && i+1 < n && s[i+1] == '-' && (i+2 >= n || isSpace(s[i+2])) {
			for i < n && s[i] != '\n' {
				sp.buf.WriteByte(s[i])
				i++
			}
			continue
		}
		if c == '#' {
			for i < n && s[i] != '\n' {
				sp.buf.WriteByte(s[i])
				i++
			}
			continue
		}
		if c == '/' && i+1 < n && s[i+1] == '*' {
			sp.buf.WriteString("/*")
			sp.state = stBlock
			i = sp.consumeBlock(s, i+2)
			if sp.state != stNormal {
				return
			}
			continue
		}

		// Statement delimiter (normal state only).
		if matchAt(s, i, sp.delim) {
			sp.emitStmt()
			i += len(sp.delim)
			continue
		}

		// Quoted string / identifier.
		if c == '\'' || c == '"' || c == '`' {
			sp.buf.WriteByte(c)
			sp.hasContent = true
			switch c {
			case '\'':
				sp.state = stSingle
			case '"':
				sp.state = stDouble
			case '`':
				sp.state = stBacktick
			}
			i = sp.consumeString(s, i+1)
			if sp.state != stNormal {
				return
			}
			continue
		}

		sp.buf.WriteByte(c)
		if !isSpace(c) {
			sp.hasContent = true
		}
		i++
	}
}

// finish flushes any trailing statement (e.g. one with no closing delimiter, or
// an unterminated string at EOF — emitted best-effort, matching a real client).
func (sp *splitter) finish() {
	if sp.err == nil {
		sp.emitStmt()
	}
}

// consumeBlock copies a block-comment body from i (state==stBlock). On `*/` it
// resets state to normal and returns the index past it; otherwise it consumes
// the rest of the chunk and leaves state==stBlock.
func (sp *splitter) consumeBlock(s string, i int) int {
	n := len(s)
	for i < n {
		if s[i] == '*' && i+1 < n && s[i+1] == '/' {
			sp.buf.WriteString("*/")
			sp.state = stNormal
			return i + 2
		}
		sp.buf.WriteByte(s[i])
		i++
	}
	return n
}

// consumeString copies a quoted literal body from i (the opening quote already
// written, sp.state set to the quote kind). Backslash escapes apply to '' and
// "" but not to backtick identifiers; a doubled quote is an escaped quote for
// all three. On the closing quote it resets state and returns the index past
// it; otherwise it consumes the rest of the chunk and keeps the quote state.
func (sp *splitter) consumeString(s string, i int) int {
	n := len(s)
	quote := quoteByte(sp.state)
	for i < n {
		ch := s[i]
		if ch == '\\' && sp.state != stBacktick && i+1 < n {
			sp.buf.WriteByte(ch)
			sp.buf.WriteByte(s[i+1])
			i += 2
			continue
		}
		if ch == quote {
			if i+1 < n && s[i+1] == quote {
				sp.buf.WriteByte(ch)
				sp.buf.WriteByte(quote)
				i += 2
				continue
			}
			sp.buf.WriteByte(ch)
			sp.state = stNormal
			return i + 1
		}
		sp.buf.WriteByte(ch)
		i++
	}
	return n
}

func (sp *splitter) emitStmt() {
	if sp.hasContent {
		if t := strings.TrimSpace(sp.buf.String()); t != "" {
			if err := sp.emit(t); err != nil {
				sp.err = err
			}
		}
	}
	sp.buf.Reset()
	sp.hasContent = false
}

// tryDelimiter recognizes a `DELIMITER <token>` directive at index i. On success
// it returns the new delimiter and the index at the start of the next line (the
// whole directive line is consumed and never sent to the server).
func tryDelimiter(s string, i int) (newDelim string, next int, ok bool) {
	const kw = "delimiter"
	if i+len(kw) > len(s) || !strings.EqualFold(s[i:i+len(kw)], kw) {
		return "", 0, false
	}
	j := i + len(kw)
	if j >= len(s) || !isSpace(s[j]) {
		return "", 0, false
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	start := j
	for j < len(s) && !isSpace(s[j]) {
		j++
	}
	if start == j {
		return "", 0, false
	}
	nd := s[start:j]
	for j < len(s) && s[j] != '\n' {
		j++
	}
	if j < len(s) {
		j++ // consume the newline
	}
	return nd, j, true
}

func quoteByte(st scanState) byte {
	switch st {
	case stSingle:
		return '\''
	case stDouble:
		return '"'
	case stBacktick:
		return '`'
	}
	return 0
}

func matchAt(s string, i int, sub string) bool {
	return i+len(sub) <= len(s) && s[i:i+len(sub)] == sub
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}
