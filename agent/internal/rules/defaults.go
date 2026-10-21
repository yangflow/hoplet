package rules

// The path sets below are shared by a built-in default and the compiled-in floor.
var (
	providerConfigPaths = []string{
		"{hoplet_home}/**",
		"~/.claude/settings*.json",
		"**/.claude/settings*.json",
		"~/.codex/config.toml",
		"**/.codex/config.toml",
	}
	credentialPaths = []string{
		"~/.ssh/**", "~/.aws/**", "~/.gnupg/**", "~/.config/gcloud/**", "~/.kube/**",
		"~/.docker/config.json", "~/.netrc", "~/.npmrc", "~/.git-credentials",
	}
)

// Defaults returns the built-in rules, all enabled. The user can turn one off and cannot edit it;
// the caller keeps the switches.
func Defaults() []Rule {
	b := func(id, tool, note string, a Action, m Match) Rule {
		return Rule{ID: id, Tool: tool, Match: m, Action: a, Scope: ScopeGlobal, Note: note, Enabled: true, Builtin: true}
	}
	return []Rule{
		b("default.secret-files", "read,write", "Secret files (.env)", Deny, Match{
			Paths:  []string{"**/.env", "**/.env.*"},
			Except: []string{"**/*.example", "**/*.sample", "**/*.template"},
		}),
		b("default.credential-stores", "read,write", "Credential stores", Deny, Match{Paths: credentialPaths}),
		b("default.hoplet-and-provider-config", "read,write", "Hoplet and provider configuration", Deny,
			Match{Paths: providerConfigPaths}),
		b("default.write-outside-workspace", "write", "Writes outside the workspace", Deny, Match{
			Paths:  []string{"/**"},
			Except: []string{"{workspace}/**", "{tmp}/**", "~/.claude/plans/**"},
		}),
		b("default.force-push", "run", "Force push", Ask, Match{
			Command: "git push",
			AnyArg:  []string{"--force*", "-f", "+*"},
		}),
		b("default.destructive-git", "run", "Destructive git commands", Ask, Match{Or: []Match{
			{Command: "git reset --hard"},
			{Command: "git clean", AnyArg: []string{"-f", "-fd", "-df", "-fx", "--force"}},
			{Command: "git checkout --"},
		}}),
		b("default.recursive-delete-and-sudo", "run", "Recursive delete and privilege", Ask, Match{Or: []Match{
			{Command: "rm", AnyArg: []string{"-r", "-R", "-rf", "-fr", "--recursive"}},
			{Command: "sudo"},
			{Command: "doas"},
			{Command: "su"},
		}}),
		b("default.download-and-run", "run", "Download and run", Ask, Match{Or: []Match{
			{Command: "curl", PipedTo: []string{"sh", "bash", "zsh", "python*", "node"}},
			{Command: "wget", PipedTo: []string{"sh", "bash", "zsh", "python*", "node"}},
		}}),
	}
}

// Floor is the rule compiled into the agent: a deny of the Hoplet and provider configuration
// that no push and no setting turns off.
func Floor() Rule {
	return Rule{
		ID:      "floor.hoplet-and-provider-config",
		Tool:    "read,write",
		Match:   Match{Paths: providerConfigPaths},
		Action:  Deny,
		Scope:   ScopeAgent,
		Note:    "Hoplet and provider configuration (agent floor)",
		Enabled: true,
		Builtin: true,
	}
}
