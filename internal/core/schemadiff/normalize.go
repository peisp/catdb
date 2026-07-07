package schemadiff

import (
	"regexp"
	"strings"
)

// Native-type normalization so cosmetic differences ("decimal(10, 2)" vs
// "DECIMAL(10,2)") don't produce bogus MODIFY statements. This fallback is
// dialect-neutral: uppercase base type, whitespace-free param list. Database
// quirks beyond that (MySQL's UNSIGNED position, ZEROFILL noise, Postgres
// type aliases, …) belong to the driver — callers pass the driver's
// Dialect.NormalizeType via Options.NormalizeType.

var reTypeParm = regexp.MustCompile(`^([^()]+?)\s*\((.+)\)\s*$`)

// NormalizeNativeType canonicalizes a native type string for equality
// comparison: uppercase base, no whitespace inside parens.
func NormalizeNativeType(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if m := reTypeParm.FindStringSubmatch(s); m != nil {
		parts := strings.Split(strings.TrimSpace(m[2]), ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return strings.ToUpper(strings.TrimSpace(m[1])) + "(" + strings.Join(parts, ",") + ")"
	}
	return strings.ToUpper(s)
}
