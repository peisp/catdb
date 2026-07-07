package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoDialectLeaksInGenericLayers walks the generic layers' Go sources
// (internal/services, internal/core) and fails if any *string literal*
// carries hard-coded MySQL dialect — the class of leak this refactor removed
// (information_schema queries, USE statements, backtick quoting). Dialect-
// specific SQL belongs behind the dbdriver interfaces in plugins/.
//
// AST-based so comments and identifiers never trip it; only actual SQL text
// embedded in the generic layers can.
func TestNoDialectLeaksInGenericLayers(t *testing.T) {
	roots := []string{".", "../core"}
	banned := []func(s string) (string, bool){
		func(s string) (string, bool) {
			return "information_schema", strings.Contains(strings.ToLower(s), "information_schema")
		},
		func(s string) (string, bool) {
			up := strings.ToUpper(strings.TrimSpace(s))
			return "USE-statement", strings.HasPrefix(up, "USE ")
		},
		func(s string) (string, bool) {
			return "SHOW CREATE", strings.Contains(strings.ToUpper(s), "SHOW CREATE")
		},
		func(s string) (string, bool) {
			// Backtick-quoted identifier inside a SQL-looking literal.
			return "backtick-quoted SQL", strings.Contains(s, "`") &&
				(strings.Contains(strings.ToUpper(s), "SELECT") || strings.Contains(strings.ToUpper(s), "FROM"))
		},
	}

	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				for _, check := range banned {
					if name, hit := check(lit.Value); hit {
						t.Errorf("%s: %s leaked into generic layer: %s",
							fset.Position(lit.Pos()), name, lit.Value)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}
