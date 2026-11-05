package shell

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// styleMethods take sizes, spacing, radii, widths or shadows: their values
// come from the theme's tokens, never from a number written here.
var styleMethods = map[string]bool{
	"Padding": true, "Gap": true, "Radius": true, "Height": true, "Width": true, "Size": true,
	"MinWidth": true, "MaxWidth": true, "MinHeight": true, "MaxHeight": true, "Margin": true,
	"FontSize": true, "LineHeight": true, "LetterSpacing": true, "FontWeight": true,
	"Shadow": true, "Border": true, "BorderWidth": true,
}

// TestValuesComeFromTokens keeps the views to the design system: a number
// in a style, a color, or a duration written in the views fails, so the
// canvas and the app can only differ where the tokens do.
func TestValuesComeFromTokens(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var bad []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			where := pos.Filename + ":" + itoa(pos.Line)
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "ui" && (sel.Sel.Name == "Hex" || sel.Sel.Name == "RGBA" || sel.Sel.Name == "RGB") {
				bad = append(bad, where+": a color written here; use the theme's colors")
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" {
				return true
			}
			if styleMethods[sel.Sel.Name] {
				for _, arg := range call.Args {
					if lit := number(arg); lit != "" {
						bad = append(bad, where+": "+sel.Sel.Name+"("+lit+"); use a token")
						break
					}
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			// A named constant is a rule of behavior, as how soon a second
			// ⎋ must come; motion is written in place and comes from tokens.
			if d, ok := n.(*ast.GenDecl); ok && d.Tok == token.CONST {
				return false
			}
			b, ok := n.(*ast.BinaryExpr)
			if !ok || b.Op != token.MUL {
				return true
			}
			if s, ok := b.Y.(*ast.SelectorExpr); ok {
				if id, ok := s.X.(*ast.Ident); ok && id.Name == "time" && (s.Sel.Name == "Millisecond" || s.Sel.Name == "Second") {
					if lit := number(b.X); lit != "" {
						pos := fset.Position(b.Pos())
						bad = append(bad, pos.Filename+":"+itoa(pos.Line)+": a duration written here; use a motion token")
					}
				}
			}
			return true
		})
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Error(b)
	}
}

// number reports a literal number other than 0 in e, the way it is written.
func number(e ast.Expr) string {
	found := ""
	ast.Inspect(e, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		switch v := n.(type) {
		case *ast.BasicLit:
			if (v.Kind == token.INT || v.Kind == token.FLOAT) && v.Value != "0" {
				found = v.Value
			}
		case *ast.CallExpr:
			// A call's own arguments are checked where it is a style method.
			return false
		case *ast.BinaryExpr:
			// A share of a token, as 0.75 of a column, is a proportion.
			if l, ok := v.X.(*ast.BasicLit); ok && v.Op == token.MUL && l.Kind == token.FLOAT && strings.HasPrefix(l.Value, "0.") {
				return false
			}
			// A count of a token, as two spaces.
			if l, ok := v.X.(*ast.BasicLit); ok && v.Op == token.MUL && l.Kind == token.INT {
				if _, lit := v.Y.(*ast.BasicLit); !lit {
					return false
				}
			}
			// Half of a size, as the radius of a circle.
			if l, ok := v.Y.(*ast.BasicLit); ok && v.Op == token.QUO && l.Value == "2" {
				return false
			}
		}
		return true
	})
	return found
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
