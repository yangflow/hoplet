package main

import (
	"bytes"
	"strings"
	"testing"
)

func generate(t *testing.T, src string) (string, error) {
	t.Helper()
	m, err := parseSources([]source{{"a.go", []byte("package proto\n" + src)}})
	if err != nil {
		return "", err
	}
	out, err := emit(m)
	return string(out), err
}

func TestMapping(t *testing.T) {
	const imports = `import ("encoding/json"; "time")
var _ json.RawMessage; var _ time.Time
`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"scalars", "type A struct { N string `json:\"n\"`; B bool `json:\"b\"`; C uint64 `json:\"c\"` }", []string{
			"public var n: String", "public var b: Bool", "public var c: UInt64",
			"n = try c.decodeOrZero(String.self, forKey: .n)", "try c.encode(n, forKey: .n)",
		}},
		{"omitempty", "type A struct { T uint32 `json:\"turn,omitempty\"` }", []string{"try c.encodeUnlessZero(turn, forKey: .turn)"}},
		{"pointer", "type A struct { P *bool `json:\"p,omitempty\"`; Q *bool `json:\"q\"` }", []string{
			"public var p: Bool?", "p = try c.decodeIfPresent(Bool.self, forKey: .p)",
			"try c.encodeIfPresent(p, forKey: .p)", "try c.encode(q, forKey: .q)", "p: Bool? = nil",
		}},
		{"slices and bytes", "type A struct { S []string `json:\"s\"`; D []byte `json:\"d\"` }", []string{"public var s: [String]", "public var d: Data"}},
		{"wire types", imports + "type A struct { T time.Time `json:\"ts\"`; R json.RawMessage `json:\"r\"` }", []string{"public var ts: Date", "public var r: JSONValue"}},
		{"camel case and keyword", "type A struct { X string `json:\"added_at\"`; Y string `json:\"default\"` }", []string{
			"public var addedAt: String", `case addedAt = "added_at"`, "public var `default`: String", "self.`default` = `default`",
		}},
		{"skipped fields", "type A struct { x string; Y string `json:\"-\"`; Z string `json:\"z\"` }", []string{"public var z: String"}},
		{"identifier string", "type ID string\ntype A struct { I ID `json:\"i\"` }", []string{"public struct ID: RawRepresentable", "public var i: ID"}},
		{"enum", "type M string\nconst (\nMA M = \"read-only\"\nMB M = \"default\"\nMC M = \"\"\n)", []string{
			"public enum M:", "case readOnly", "case `default`", "case empty", "case unknown(String)",
			`case "read-only": self = .readOnly`, "default: self = .unknown(rawValue)", `case .readOnly: return "read-only"`,
		}},
		{"empty struct", "type A struct{}", []string{"public struct A:", "public init() {}"}},
		{"event", "type T string\nconst TA T = \"turn.started\"\ntype EventType string\ntype Started struct{}\nfunc (Started) eventType() T { return TA }\nfunc (Started) eventVersion() uint16 { return 1 }", []string{
			"case started(Started)", `case ("turn.started", 1): return .started(try payload.decode(Started.self))`, "case unknown(type: EventType, v: UInt16)",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := generate(t, tt.src)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
		})
	}
}

func TestUnsupportedShapes(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"map field", "type A struct { M map[string]string `json:\"m\"` }", "a.go:2"},
		{"float", "type A struct { F float64 `json:\"f\"` }", "unsupported type float64"},
		{"unknown named type", "type A struct { F Missing `json:\"f\"` }", "unsupported type Missing"},
		{"foreign type", "type A struct { F net.IP `json:\"f\"` }", "unsupported type net.IP"},
		{"embedded", "type B struct{}\ntype A struct { B }", "embedded"},
		{"no tag", "type A struct { F string }", "missing json tag"},
		{"two names", "type A struct { F, G string `json:\"f\"` }", "one field per line"},
		{"string option", "type A struct { F int64 `json:\"f,string\"` }", "unsupported json option"},
		{"int-based type", "type A int", "only string-based"},
		{"map type", "type A map[string]string", "unsupported declaration"},
		{"non-literal constant", "type M string\nconst x = \"a\"\nconst MA M = x", "string literal"},
		{"case clash", "type M string\nconst (\nMA M = \"a-b\"\nMB M = \"a.b\"\n)", "taken by"},
		{"reserved case", "type M string\nconst MA M = \"unknown\"", "fallback case"},
		{"event without version", "type T string\nconst TA T = \"x\"\ntype S struct{}\nfunc (S) eventType() T { return TA }", "no eventVersion"},
		{"event with unknown constant", "type S struct{}\nfunc (S) eventType() string { return Nope }\nfunc (S) eventVersion() uint16 { return 1 }", "unknown constant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generate(t, tt.src)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestOutputIsStableAcrossInputOrder(t *testing.T) {
	a := source{"a.go", []byte("package proto\ntype A struct { X string `json:\"x\"` }")}
	b := source{"b.go", []byte("package proto\ntype B struct { Y string `json:\"y\"` }")}
	render := func(s ...source) []byte {
		m, err := parseSources(s)
		if err != nil {
			t.Fatal(err)
		}
		out, err := emit(m)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if !bytes.Equal(render(a, b), render(b, a)) {
		t.Fatal("output depends on input order")
	}
}

func TestRealProtoPackageGenerates(t *testing.T) {
	sources, err := readSources("../../proto")
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseSources(sources)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emit(m); err != nil {
		t.Fatal(err)
	}
	if len(m.events) == 0 {
		t.Fatal("no events found")
	}
}
