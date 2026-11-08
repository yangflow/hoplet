package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// model is everything the generator reads from the proto package, in file
// and declaration order so that the output is stable.
type model struct {
	structs []structDecl
	strs    []stringDecl
	events  []eventDecl
}

type structDecl struct {
	name   string
	fields []fieldDecl
}

// fieldDecl is one JSON field. typ is the Go type as written, parsed.
type fieldDecl struct {
	jsonName  string
	omitEmpty bool
	typ       ast.Expr
	pos       token.Pos
}

// stringDecl is a defined string type. values lists the distinct constants
// declared with that type; none means the type is an opaque identifier.
type stringDecl struct {
	name   string
	values []string
}

// eventDecl ties a payload struct to its event type and version.
type eventDecl struct {
	payload   string
	eventType string
	version   int
}

type source struct {
	name string
	text []byte
}

// readSources returns the non-test Go files of dir, sorted by name.
func readSources(dir string) ([]source, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []source
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		text, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a build-time tool reading the directory it is given
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out = append(out, source{name, text})
	}
	return out, nil
}

// parseSources builds the model. Any shape the mapping does not cover is an
// error naming its source position, never silently skipped.
func parseSources(sources []source) (*model, error) {
	fset := token.NewFileSet()
	sorted := slices.Clone(sources)
	slices.SortFunc(sorted, func(a, b source) int { return strings.Compare(a.name, b.name) })

	var files []*ast.File
	for _, s := range sorted {
		f, err := parser.ParseFile(fset, s.name, s.text, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse: %w", err)
		}
		files = append(files, f)
	}

	p := &parseState{fset: fset, m: &model{}, stringIndex: map[string]int{}, consts: map[string]string{}}
	for _, f := range files {
		if err := p.types(f); err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		if err := p.constants(f); err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		if err := p.eventMethods(f); err != nil {
			return nil, err
		}
	}
	if err := p.finishEvents(); err != nil {
		return nil, err
	}
	if err := p.checkFieldTypes(); err != nil {
		return nil, err
	}
	return p.m, nil
}

type parseState struct {
	fset        *token.FileSet
	m           *model
	stringIndex map[string]int    // string type name → index in m.strs
	consts      map[string]string // typed string constant → value
	eventTypes  map[string]string // payload → constant name
	versions    map[string]int    // payload → version
	eventOrder  []string
}

func (p *parseState) errorf(pos token.Pos, format string, args ...any) error {
	return fmt.Errorf("%s: %s", p.fset.Position(pos), fmt.Sprintf(format, args...))
}

func (p *parseState) types(f *ast.File) error {
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !ts.Name.IsExported() {
				continue
			}
			if err := p.typeSpec(ts); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *parseState) typeSpec(ts *ast.TypeSpec) error {
	name := ts.Name.Name
	switch t := ts.Type.(type) {
	case *ast.StructType:
		decl := structDecl{name: name}
		for _, field := range t.Fields.List {
			fd, skip, err := p.field(name, field)
			if err != nil {
				return err
			}
			if !skip {
				decl.fields = append(decl.fields, fd)
			}
		}
		p.m.structs = append(p.m.structs, decl)
		return nil
	case *ast.Ident:
		if t.Name != "string" {
			return p.errorf(ts.Pos(), "type %s: only string-based defined types are supported", name)
		}
		p.stringIndex[name] = len(p.m.strs)
		p.m.strs = append(p.m.strs, stringDecl{name: name})
		return nil
	case *ast.InterfaceType:
		return nil // Payload is represented by the generated EventPayload.
	default:
		return p.errorf(ts.Pos(), "type %s: unsupported declaration", name)
	}
}

// field reads one struct field. skip reports a field that is not on the wire.
func (p *parseState) field(owner string, f *ast.Field) (fd fieldDecl, skip bool, err error) {
	if len(f.Names) == 0 {
		return fd, false, p.errorf(f.Pos(), "%s: embedded fields are not supported", owner)
	}
	if len(f.Names) > 1 {
		return fd, false, p.errorf(f.Pos(), "%s: declare one field per line", owner)
	}
	name := f.Names[0]
	if !name.IsExported() {
		return fd, true, nil
	}
	if f.Tag == nil {
		return fd, false, p.errorf(f.Pos(), "%s.%s: missing json tag", owner, name.Name)
	}
	tagText, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return fd, false, p.errorf(f.Pos(), "%s.%s: bad tag", owner, name.Name)
	}
	tag, ok := reflect.StructTag(tagText).Lookup("json")
	if !ok {
		return fd, false, p.errorf(f.Pos(), "%s.%s: missing json tag", owner, name.Name)
	}
	if tag == "-" {
		return fd, true, nil
	}
	jsonName, opts, _ := strings.Cut(tag, ",")
	if jsonName == "" {
		return fd, false, p.errorf(f.Pos(), "%s.%s: json tag has no name", owner, name.Name)
	}
	omitEmpty := false
	switch opts {
	case "":
	case "omitempty":
		omitEmpty = true
	default:
		return fd, false, p.errorf(f.Pos(), "%s.%s: unsupported json option %q", owner, name.Name, opts)
	}
	return fieldDecl{jsonName: jsonName, omitEmpty: omitEmpty, typ: f.Type, pos: f.Pos()}, false, nil
}

func (p *parseState) constants(f *ast.File) error {
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			typ, ok := vs.Type.(*ast.Ident)
			if !ok {
				continue
			}
			idx, known := p.stringIndex[typ.Name]
			if !known {
				continue
			}
			if len(vs.Names) != len(vs.Values) {
				return p.errorf(vs.Pos(), "constant of type %s needs an explicit value", typ.Name)
			}
			for i, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return p.errorf(v.Pos(), "constant of type %s must be a string literal", typ.Name)
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					return p.errorf(v.Pos(), "bad string literal")
				}
				p.consts[vs.Names[i].Name] = value
				decl := &p.m.strs[idx]
				if !slices.Contains(decl.values, value) {
					decl.values = append(decl.values, value)
				}
			}
		}
	}
	return nil
}

func (p *parseState) eventMethods(f *ast.File) error {
	if p.eventTypes == nil {
		p.eventTypes = map[string]string{}
		p.versions = map[string]int{}
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		if fn.Name.Name != "eventType" && fn.Name.Name != "eventVersion" {
			continue
		}
		recv, ok := fn.Recv.List[0].Type.(*ast.Ident)
		if !ok {
			return p.errorf(fn.Pos(), "%s must have a value receiver", fn.Name.Name)
		}
		if fn.Body == nil || len(fn.Body.List) != 1 {
			return p.errorf(fn.Pos(), "%s.%s must be a single return", recv.Name, fn.Name.Name)
		}
		ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return p.errorf(fn.Pos(), "%s.%s must be a single return", recv.Name, fn.Name.Name)
		}
		if fn.Name.Name == "eventType" {
			id, ok := ret.Results[0].(*ast.Ident)
			if !ok {
				return p.errorf(fn.Pos(), "%s.eventType must return a constant", recv.Name)
			}
			if _, seen := p.eventTypes[recv.Name]; !seen {
				p.eventOrder = append(p.eventOrder, recv.Name)
			}
			p.eventTypes[recv.Name] = id.Name
			continue
		}
		lit, ok := ret.Results[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return p.errorf(fn.Pos(), "%s.eventVersion must return an integer literal", recv.Name)
		}
		v, err := strconv.Atoi(lit.Value)
		if err != nil {
			return p.errorf(fn.Pos(), "%s.eventVersion: %v", recv.Name, err)
		}
		p.versions[recv.Name] = v
	}
	return nil
}

var errNoVersion = errors.New("has eventType but no eventVersion")

func (p *parseState) finishEvents() error {
	for _, payload := range p.eventOrder {
		version, ok := p.versions[payload]
		if !ok {
			return fmt.Errorf("%s: %w", payload, errNoVersion)
		}
		constant := p.eventTypes[payload]
		value, ok := p.consts[constant]
		if !ok {
			return fmt.Errorf("%s: eventType returns unknown constant %s", payload, constant)
		}
		if !slices.ContainsFunc(p.m.structs, func(s structDecl) bool { return s.name == payload }) {
			return fmt.Errorf("%s: event payload is not an exported struct", payload)
		}
		p.m.events = append(p.m.events, eventDecl{payload: payload, eventType: value, version: version})
	}
	return nil
}

// checkFieldTypes rejects any field the emitter could not map.
func (p *parseState) checkFieldTypes() error {
	names := map[string]bool{}
	for _, s := range p.m.structs {
		names[s.name] = true
	}
	for _, s := range p.m.strs {
		names[s.name] = true
	}
	for _, s := range p.m.structs {
		for _, f := range s.fields {
			if _, err := swiftType(f.typ, names); err != nil {
				return p.errorf(f.pos, "%s.%s: %v", s.name, f.jsonName, err)
			}
		}
	}
	return nil
}
