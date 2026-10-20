package configs

import "sort"

// Preset is a known model service that speaks the Claude Code protocol: its endpoint and model variables, and
// the name of the variable that holds the secret, which the preset never carries a value for.
type Preset struct {
	Name string
	// Env holds the variables a preset sets, by name.
	Env map[string]string
	// Secret names the variable the owner fills with `hoplet-agent provider env`.
	Secret string
}

// presets is the closed list (ADR 0028): the Anthropic-compatible endpoints the services document for Claude Code.
var presets = map[string]Preset{
	"glm": {
		Name: "GLM (BigModel)",
		Env: map[string]string{
			"ANTHROPIC_BASE_URL":             "https://open.bigmodel.cn/api/anthropic",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   "glm-5.3[1m]",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3[1m]",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "glm-5.3-flash[1m]",
		},
		Secret: "ANTHROPIC_AUTH_TOKEN",
	},
	"zai": {
		Name: "GLM (Z.ai)",
		Env: map[string]string{
			"ANTHROPIC_BASE_URL":             "https://api.z.ai/api/anthropic",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   "glm-5.3[1m]",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3[1m]",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "glm-5.3-flash[1m]",
		},
		Secret: "ANTHROPIC_AUTH_TOKEN",
	},
	"deepseek": {
		Name: "DeepSeek",
		Env: map[string]string{
			"ANTHROPIC_BASE_URL":             "https://api.deepseek.com/anthropic",
			"ANTHROPIC_MODEL":                "deepseek-flash[1m]",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   "deepseek-flash[1m]",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-flash[1m]",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "deepseek-flash",
		},
		Secret: "ANTHROPIC_AUTH_TOKEN",
	},
	"minimax": {
		Name:   "MiniMax",
		Env:    minimaxEnv("https://api.minimax.io/anthropic"),
		Secret: "ANTHROPIC_AUTH_TOKEN",
	},
	"minimax-cn": {
		Name:   "MiniMax (China)",
		Env:    minimaxEnv("https://api.minimaxi.com/anthropic"),
		Secret: "ANTHROPIC_AUTH_TOKEN",
	},
	// The model variables of Qwen are named without a value: Model Studio's models and endpoints differ by region and
	// plan, so the owner sets them with `hoplet-agent provider env`.
	"qwen": {
		Name: "Qwen",
		Env: map[string]string{
			"ANTHROPIC_BASE_URL":             "https://dashscope.aliyuncs.com/apps/anthropic",
			"ANTHROPIC_MODEL":                "",
			"ANTHROPIC_DEFAULT_OPUS_MODEL":   "",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "",
		},
		Secret: "ANTHROPIC_AUTH_TOKEN",
	},
}

// minimaxEnv is the variables of MiniMax's documented Claude Code setup at baseURL: one model for every tier.
func minimaxEnv(baseURL string) map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL":             baseURL,
		"ANTHROPIC_MODEL":                "MiniMax-M3",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   "MiniMax-M3",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "MiniMax-M3",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "MiniMax-M3",
	}
}

// PresetNames is the names of the presets, sorted.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PresetByName is the preset called name.
func PresetByName(name string) (Preset, bool) {
	p, ok := presets[name]
	return p, ok
}

// Apply sets the preset's variables on c and names the secret variable without a value. The preset is for
// Claude Code, so c must extend it.
func (p Preset) Apply(c *Configuration) {
	if c.Env == nil {
		c.Env = map[string]string{}
	}
	for k, v := range p.Env {
		c.Env[k] = v
	}
	if _, ok := c.Env[p.Secret]; !ok {
		c.Env[p.Secret] = ""
	}
}
