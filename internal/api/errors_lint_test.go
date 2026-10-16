package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// verbRe finds the formatting verbs of a message template.
var verbRe = regexp.MustCompile(`%[^%]`)

// TestErrorMessagesAreTemplates scans the package's non-test source so that every error
// response is built from a code constant and a fixed message template (job 02): every
// newError call names a code* and a msg* constant, apiError literals appear only inside
// newError, every writeError call passes an apiError built that way (or a variable of
// that type), and every template interpolates at most one %s or %d.
func TestErrorMessagesAreTemplates(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}

	consts := map[string]string{} // package-level string constants
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							consts[name.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
		}
	}
	nTemplates := 0
	for name, value := range consts {
		if !strings.HasPrefix(name, "msg") {
			continue
		}
		nTemplates++
		verbs := verbRe.FindAllString(value, -1)
		if len(verbs) > 1 || len(verbs) == 1 && verbs[0] != "%s" && verbs[0] != "%d" {
			t.Errorf("template %s = %q: at most one %%s or %%d", name, value)
		}
	}
	if nTemplates == 0 {
		t.Fatal("no msg* templates found; is the scan reading the package?")
	}
	isConst := func(e ast.Expr, prefix string) bool {
		id, ok := e.(*ast.Ident)
		if !ok {
			return false
		}
		_, declared := consts[id.Name]
		return declared && strings.HasPrefix(id.Name, prefix)
	}
	// fromNewError reports whether e is newError(...) or a method chain on it.
	var fromNewError func(ast.Expr) bool
	fromNewError = func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			return fn.Name == "newError"
		case *ast.SelectorExpr:
			return fromNewError(fn.X)
		}
		return false
	}

	nWrites := 0
	for _, f := range files {
		var inNewError bool
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			inNewError = ok && fd.Name.Name == "newError" && fd.Recv == nil
			ast.Inspect(d, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CompositeLit:
					if id, ok := n.Type.(*ast.Ident); ok && id.Name == "apiError" && !inNewError {
						t.Errorf("%s: apiError literal outside newError", fset.Position(n.Pos()))
					}
				case *ast.CallExpr:
					id, ok := n.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					pos := fset.Position(n.Pos())
					switch id.Name {
					case "newError":
						if len(n.Args) < 2 || !isConst(n.Args[0], "code") || !isConst(n.Args[1], "msg") {
							t.Errorf("%s: newError must take a code* constant and a msg* template", pos)
						}
					case "writeError":
						nWrites++
						if len(n.Args) != 3 {
							t.Errorf("%s: writeError takes (w, r, apiError)", pos)
							return true
						}
						if _, isIdent := n.Args[2].(*ast.Ident); !isIdent && !fromNewError(n.Args[2]) {
							t.Errorf("%s: writeError's error must come from newError", pos)
						}
					}
				}
				return true
			})
		}
	}
	if nWrites == 0 {
		t.Fatal("no writeError calls found; is the scan reading the package?")
	}
}
