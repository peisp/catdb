package schemadiff

import (
	"regexp"
	"strings"
)

// Native-type normalization so cosmetic differences ("decimal(10, 2)" vs
// "DECIMAL(10,2)", trailing UNSIGNED order, ZEROFILL noise) don't produce
// bogus MODIFY statements. Ported from the front-end alterPlan.ts
// parseNativeType/buildNativeType pair; MySQL-flavored but harmless for other
// dialects (callers may swap it via Options.NormalizeType).

var (
	reZerofill = regexp.MustCompile(`(?i)\s+ZEROFILL\b`)
	reUnsigned = regexp.MustCompile(`(?i)\s+UNSIGNED\b`)
	reTypeParm = regexp.MustCompile(`^([^()]+?)\s*\((.+)\)\s*$`)
)

type parsedNativeType struct {
	baseType   string
	typeParams string
	unsigned   bool
}

func parseNativeType(raw string) parsedNativeType {
	s := strings.TrimSpace(raw)
	if s == "" {
		return parsedNativeType{}
	}
	s = reZerofill.ReplaceAllString(s, "")
	unsigned := false
	if reUnsigned.MatchString(s) {
		unsigned = true
		s = reUnsigned.ReplaceAllString(s, "")
	}
	s = strings.TrimSpace(s)
	if m := reTypeParm.FindStringSubmatch(s); m != nil {
		return parsedNativeType{
			baseType:   strings.ToUpper(strings.TrimSpace(m[1])),
			typeParams: strings.TrimSpace(m[2]),
			unsigned:   unsigned,
		}
	}
	return parsedNativeType{baseType: strings.ToUpper(s), unsigned: unsigned}
}

func buildNativeType(p parsedNativeType) string {
	s := p.baseType
	if params := strings.TrimSpace(p.typeParams); params != "" {
		s += "(" + normalizeParams(params) + ")"
	}
	if p.unsigned && baseTypeSupportsUnsigned(p.baseType) {
		s += " UNSIGNED"
	}
	return s
}

// normalizeParams strips whitespace around the commas inside a param list so
// "10, 2" and "10,2" compare equal.
func normalizeParams(params string) string {
	parts := strings.Split(params, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ",")
}

func baseTypeSupportsUnsigned(base string) bool {
	switch strings.ToUpper(base) {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT",
		"DECIMAL", "NUMERIC", "FLOAT", "DOUBLE", "REAL":
		return true
	}
	return false
}

// NormalizeNativeType canonicalizes a native type string for equality
// comparison: uppercase base, no whitespace inside parens, UNSIGNED suffix in
// a fixed position, ZEROFILL dropped.
func NormalizeNativeType(s string) string {
	if s == "" {
		return ""
	}
	return buildNativeType(parseNativeType(s))
}
