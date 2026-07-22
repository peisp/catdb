package agent

import (
	"regexp"
	"strings"
)

// Delivery validation (AGENT_DESIGN.md §6/§8): when the model stops calling
// tools and gives its final answer, the loop checks it against the mode's
// delivery contract. A failing answer gets one system-generated repair
// message and another round, capped at maxDeliveryRepairs; still failing →
// deliver anyway with a contract warning (never silently discard).

const maxDeliveryRepairs = 2

var (
	sqlFenceRe = regexp.MustCompile("(?is)```sql\\s.*?(select|insert|update|delete|create|alter|drop|with|show|explain)")
	anyFenceRe = regexp.MustCompile("(?s)```.*?```")
	// A line that reads like a bare SQL statement: leading verb + a structural
	// keyword later in the line. High precision beats recall here — prose
	// answers must never be bounced.
	bareSQLRe = regexp.MustCompile(`(?im)^\s*(SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP)\b.+\b(FROM|INTO|SET|TABLE|VALUES|WHERE)\b`)
)

// deliveryVerdict is the programmatic check result for one final answer.
type deliveryVerdict struct {
	OK      bool
	Missing string // what the repair message should ask for
}

// validateDelivery checks a final answer against the mode's delivery contract
// (§6/§8). The checks are deliberately high-precision — a chatty but
// legitimate answer must never be bounced:
//   - empty answers fail everywhere;
//   - ask:   SQL written as plain text outside any code fence fails — the
//     ```sql block is the mode's core deliverable (copy/insert/open actions);
//   - agent: an answer that presents a ```sql block without having executed
//     anything this task fails — data answers come from real run_sql results,
//     the model must not stop at SQL text (§6).
//
// Pure function — unit-tested directly.
func validateDelivery(mode, finalText string, ranSQL bool) deliveryVerdict {
	text := strings.TrimSpace(finalText)
	if text == "" {
		return deliveryVerdict{Missing: "an actual answer (the reply was empty)"}
	}
	switch mode {
	case "ask":
		unfenced := anyFenceRe.ReplaceAllString(text, "")
		if bareSQLRe.MatchString(unfenced) {
			return deliveryVerdict{Missing: "the SQL wrapped in a ```sql code block (it is currently plain text)"}
		}
	case "agent":
		if sqlFenceRe.MatchString(text) && !ranSQL {
			return deliveryVerdict{Missing: "actual execution: run the statement with run_sql (after plan approval if it writes) and answer from its real result — do not stop at SQL text"}
		}
	}
	return deliveryVerdict{OK: true}
}

// repairMessage is the system-generated fix-it message fed back as a user
// turn (in-memory only — it is loop plumbing, not conversation history).
func repairMessage(missing string) string {
	return "[system delivery check] Your previous reply does not fulfil the delivery contract. Missing: " +
		missing + ". Produce the complete final answer now."
}
