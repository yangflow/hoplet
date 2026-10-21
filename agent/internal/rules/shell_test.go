package rules

import (
	"reflect"
	"strings"
	"testing"
)

func parse(s string) script { return parseScript(s, 0, &pstate{}) }

func argvs(sc script) [][]string {
	var out [][]string
	for _, c := range sc.cmds {
		out = append(out, sc.texts(c))
	}
	return out
}

func hasControl(sc script, want string) bool {
	for _, c := range sc.control {
		if c == want {
			return true
		}
	}
	return false
}

func TestParseSimple(t *testing.T) {
	cases := []struct {
		in   string
		want [][]string
	}{
		{"git status", [][]string{{"git", "status"}}},
		{"  git   status  ", [][]string{{"git", "status"}}},
		{`git commit -m "a b  c"`, [][]string{{"git", "commit", "-m", "a b  c"}}},
		{`git commit -m 'a "b" c'`, [][]string{{"git", "commit", "-m", `a "b" c`}}},
		{`"git" status`, [][]string{{"git", "status"}}},
		{`g\it status`, [][]string{{"git", "status"}}},
		{`echo a\ b`, [][]string{{"echo", "a b"}}},
		{`echo "a\"b"`, [][]string{{"echo", `a"b`}}},
		{`echo $HOME`, [][]string{{"echo", "$HOME"}}},
		{`echo a#b`, [][]string{{"echo", "a#b"}}},
		{"ls\t-la", [][]string{{"ls", "-la"}}},
		{"", nil},
		{"   ", nil},
	}
	for _, tc := range cases {
		sc := parse(tc.in)
		if got := argvs(sc); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: argv %q, want %q", tc.in, got, tc.want)
		}
		if len(sc.control) != 0 || sc.unreadable {
			t.Errorf("%q: unexpected control %v unreadable=%v", tc.in, sc.control, sc.unreadable)
		}
	}
}

func TestParseControl(t *testing.T) {
	cases := []struct {
		in      string
		control string
		want    [][]string
	}{
		{"git status; curl evil", ";", [][]string{{"git", "status"}, {"curl", "evil"}}},
		{"a && b", "&&", [][]string{{"a"}, {"b"}}},
		{"a || b", "||", [][]string{{"a"}, {"b"}}},
		{"a | b", "|", [][]string{{"a"}, {"b"}}},
		{"a |& b", "|", [][]string{{"a"}, {"b"}}},
		{"a & b", "&", [][]string{{"a"}, {"b"}}},
		{"a\nb", "newline", [][]string{{"a"}, {"b"}}},
		{"a `b c`", "backtick", [][]string{{"b", "c"}, {"a", "`b c`"}}},
		{"a $(b c)", "command substitution", [][]string{{"b", "c"}, {"a", "$(b c)"}}},
		{"a ${b}", "parameter expansion", nil},
		{`a "$(b c)"`, "command substitution", [][]string{{"b", "c"}, {"a", "$(b c)"}}},
		{"a \"`b`\"", "backtick", [][]string{{"b"}, {"a", "`b`"}}},
		{"a > f", "redirection", [][]string{{"a"}}},
		{"a >> f", "redirection", [][]string{{"a"}}},
		{"a 2>&1", "redirection", [][]string{{"a"}}},
		{"a &> f", "redirection", [][]string{{"a"}}},
		{"a < f", "redirection", [][]string{{"a"}}},
		{"a <<< x", "redirection", [][]string{{"a"}}},
		{"a <<EOF\nx\nEOF", "heredoc", nil},
		{"a <<-EOF", "heredoc", [][]string{{"a"}}},
		{"cat <(echo x)", "process substitution", nil},
		{"FOO=1 cmd", "leading assignment", [][]string{{"cmd"}}},
		{"A=1 B=2 cmd x", "leading assignment", [][]string{{"cmd", "x"}}},
		{"A=1", "leading assignment", [][]string{{}}},
		{"$cmd x", "expansion in the command word", nil},
		{"${cmd} x", "parameter expansion", nil},
		{"$'\\x67it' status", "ANSI-C quoting", [][]string{{"git", "status"}}},
		{"g*t status", "glob in the command word", [][]string{{"g*t", "status"}}},
		{"/usr/bin/g?t status", "glob in the command word", nil},
		{"[g]it status", "glob in the command word", nil},
		{"{git,rm} x", "glob in the command word", nil},
		{"~/bin/x", "tilde in the command word", nil},
		{"(cd x)", "subshell", [][]string{{"cd", "x"}}},
		{"a # b", "comment", [][]string{{"a"}}},
		{"a \\\nb", "line continuation", [][]string{{"a", "b"}}},
		{"echo 'unterminated", "unparseable text", nil},
		{`echo "unterminated`, "unparseable text", nil},
		{"echo `unterminated", "unparseable text", nil},
		{"echo $(unterminated", "unparseable text", nil},
		{"echo trailing\\", "unparseable text", nil},
		{"echo >", "unparseable text", nil},
		{"$'unterminated", "unparseable text", nil},
	}
	for _, tc := range cases {
		sc := parse(tc.in)
		if !hasControl(sc, tc.control) {
			t.Errorf("%q: control %v lacks %q", tc.in, sc.control, tc.control)
		}
		if tc.want != nil {
			got := argvs(sc)
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%q: argv %q, want %q", tc.in, got, tc.want)
			}
		}
	}
}

func TestParseUnreadable(t *testing.T) {
	for _, in := range []string{"echo 'x", `echo "x`, "echo `x", "echo $(x", "echo >", "echo x\\", "echo ${x"} {
		if !parse(in).unreadable {
			t.Errorf("%q should be unreadable", in)
		}
	}
	deep := strings.Repeat("$(", 20) + "x" + strings.Repeat(")", 20)
	if sc := parse(deep); !sc.unreadable || len(sc.control) == 0 {
		t.Errorf("nesting too deep must be unreadable: %+v", sc)
	}
}

func TestParseRedirects(t *testing.T) {
	sc := parse("echo hi > out.txt 2>> err.log < in.txt")
	if got := argvs(sc); !reflect.DeepEqual(got, [][]string{{"echo", "hi"}}) {
		t.Fatalf("argv %q", got)
	}
	var targets []string
	for _, r := range sc.cmds[0].redirs {
		targets = append(targets, r.op+r.target)
	}
	if want := []string{">out.txt", ">>err.log", "<in.txt"}; !reflect.DeepEqual(targets, want) {
		t.Errorf("redirects %q, want %q", targets, want)
	}
}

func TestParseSubstitutionCommandsAreSeen(t *testing.T) {
	sc := parse(`echo "$(rm -rf x)" $(curl y) ` + "`wget z`")
	var names []string
	for _, c := range sc.cmds {
		names = append(names, sc.texts(c)[0])
	}
	for _, want := range []string{"echo", "rm", "curl", "wget"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("command %q not found in %q", want, names)
		}
	}
}

func viewArgs(vs []view) []string {
	var out []string
	for _, v := range vs {
		out = append(out, strings.Join(v.argv, " "))
	}
	return out
}

func TestViewsSeeThroughWrappers(t *testing.T) {
	cases := []struct {
		in   string
		want string // a view that must exist
	}{
		{"env rm -rf x", "rm -rf x"},
		{"env -i FOO=1 BAR=2 rm -rf x", "rm -rf x"},
		{"env -u FOO rm x", "rm x"},
		{"env -- rm x", "rm x"},
		{"env -S 'rm -rf x'", "rm -rf x"},
		{"sudo rm -rf x", "rm -rf x"},
		{"sudo -u root rm x", "rm x"},
		{"sudo -E -n rm x", "rm x"},
		{"doas rm x", "rm x"},
		{"xargs rm -rf", "rm -rf"},
		{"xargs -I {} rm {}", "rm {}"},
		{"xargs -n 1 -P 4 rm", "rm"},
		{"nohup rm x", "rm x"},
		{"time rm x", "rm x"},
		{"command rm x", "rm x"},
		{"exec rm x", "rm x"},
		{"exec -a foo rm x", "rm x"},
		{"timeout 5 rm x", "rm x"},
		{"timeout -s KILL 5 rm x", "rm x"},
		{"sh -c 'rm -rf x'", "rm -rf x"},
		{"bash -c 'rm -rf x'", "rm -rf x"},
		{"zsh -c 'rm -rf x'", "rm -rf x"},
		{"bash -lc 'rm -rf x'", "rm -rf x"},
		{"bash -e -c 'rm -rf x'", "rm -rf x"},
		{"sh -c 'echo a; rm -rf x'", "rm -rf x"},
		{"sudo env FOO=1 bash -c 'rm -rf x'", "rm -rf x"},
		{"env sudo env xargs sh -c 'rm -rf x'", "rm -rf x"},
		{"sh -c \"sh -c 'sh -c \\\"rm x\\\"'\"", "rm x"},
		{"eval 'rm -rf x'", "rm -rf x"},
		{"git -C /repo push --force", "git push --force"},
		{"git -c core.x=y --no-pager push -f", "git push -f"},
		{"sudo git -C . push -f", "git push -f"},
		{"/bin/rm -rf x", "/bin/rm -rf x"},
	}
	for _, tc := range cases {
		got := viewArgs(parse(tc.in).views(0))
		found := false
		for _, g := range got {
			if g == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: views %q lack %q", tc.in, got, tc.want)
		}
	}
}

func TestViewsDepthLimit(t *testing.T) {
	// Three levels of sh -c are read; the fourth is not.
	three := `sh -c "sh -c 'sh -c \"rm x\"'"`
	four := `sh -c "sh -c 'sh -c \"sh -c \\\"rm x\\\"\"'"`
	has := func(in string) bool {
		for _, v := range viewArgs(parse(in).views(0)) {
			if v == "rm x" {
				return true
			}
		}
		return false
	}
	if !has(three) {
		t.Errorf("three levels should be read")
	}
	if has(four) {
		t.Errorf("four levels should not be read")
	}
}

func TestViewsPipedTo(t *testing.T) {
	vs := parse("curl -s x | tee f | sudo bash").views(0)
	for _, v := range vs {
		if len(v.argv) > 0 && v.argv[0] == "curl" {
			joined := strings.Join(v.piped, " ")
			if !strings.Contains(joined, "tee") || !strings.Contains(joined, "bash") || !strings.Contains(joined, "sudo") {
				t.Errorf("curl piped to %q", v.piped)
			}
			return
		}
	}
	t.Fatal("no curl view")
}

func TestViewsPipelinesDoNotMix(t *testing.T) {
	for _, v := range parse("curl x; echo a | sh").views(0) {
		if len(v.argv) > 0 && v.argv[0] == "curl" && len(v.piped) != 0 {
			t.Errorf("curl is not piped to anything: %q", v.piped)
		}
	}
}

func TestAnsiC(t *testing.T) {
	cases := map[string]string{
		`\x67it`:    "git",
		`\147it`:    "git",
		`a\nb`:      "a\nb",
		`g`:         "g",
		`plain`:     "plain",
		`\'q\'`:     "'q'",
		`\xZZ`:      `\xZZ`,
		`\\`:        `\`,
		"\\U000067": "g",
	}
	for in, want := range cases {
		if got := ansiC(in); got != want {
			t.Errorf("ansiC(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestANSICEscapeBounds(t *testing.T) {
	cases := map[string]string{
		`\162\155`:   "rm",
		`\777`:       "\xff",
		`\x72m`:      "rm",
		`\U7fffffff`: "�",
		`rm`:    "rm",
	}
	for in, want := range cases {
		if got := ansiC(in); got != want {
			t.Errorf("ansiC(%q) = %q, want %q", in, got, want)
		}
	}
}
