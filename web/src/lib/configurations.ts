import type { ConfigOption, ConfigSetting, ProviderInfo, SessionConfig } from '@hoplet/shared';

type Vendor = ProviderInfo['provider'];

/**
 * The model services `hoplet-agent provider add --preset` knows (ADR 0028). The list is closed and mirrors
 * agent/internal/configs/presets.go; the web only shows the commands for them and never runs one.
 */
export const PRESETS: readonly { id: string; name: string }[] = [
  { id: 'glm', name: 'GLM (BigModel)' },
  { id: 'zai', name: 'GLM (Z.ai)' },
  { id: 'deepseek', name: 'DeepSeek' },
  { id: 'minimax', name: 'MiniMax' },
  { id: 'minimax-cn', name: 'MiniMax (China)' },
  { id: 'qwen', name: 'Qwen' },
];

/** The variable that holds a preset's key. */
export const SECRET_VARIABLE = 'ANTHROPIC_AUTH_TOKEN';

/** The variables that give a configuration its own login directory. */
const OWN_LOGIN = ['CLAUDE_CONFIG_DIR', 'CODEX_HOME'];

/** The vendors in the order Settings and the pickers list them. */
export const VENDORS: readonly Vendor[] = ['claude', 'codex'];

/** The command that creates a configuration from a preset on the machine. */
export function addCommand(preset: string, vendor: Vendor = 'claude'): string {
  return `hoplet-agent provider add ${preset} --extends ${vendor} --preset ${preset}`;
}

/** The command that sets a variable of a configuration on the machine; it asks for the value there. */
export function envCommand(id: string, name: string): string {
  return `hoplet-agent provider env ${id} ${name}`;
}

/** Whether the entry is the vendor's own configuration, which can be turned off but not removed. */
export function isBuiltIn(info: ProviderInfo): boolean {
  return info.configuration.id === info.provider;
}

/** The names of the variables the configuration needs and the machine does not have. */
export function missingVariables(info: ProviderInfo): string[] {
  return info.configuration.env.filter((e) => !e.set).map((e) => e.name);
}

/** Whether the configuration signs in apart from the vendor's own tool (it has its own config directory). */
export function hasOwnLogin(info: ProviderInfo): boolean {
  return info.configuration.env.some((e) => OWN_LOGIN.includes(e.name));
}

/** Enabled, but a variable has no value: a session started with it would fail. */
export function notReady(info: ProviderInfo): boolean {
  return info.configuration.enabled && missingVariables(info).length > 0;
}

/** What the configuration runs: its own command, else the vendor's. */
export function runsCommand(info: ProviderInfo): string | undefined {
  return info.configuration.command || undefined;
}

/** The entries by vendor, in the vendors' order and the machine's order within one; a vendor without entries is left out. */
export function groupByVendor(
  providers: readonly ProviderInfo[],
): { provider: Vendor; items: ProviderInfo[] }[] {
  return VENDORS.map((provider) => ({
    provider,
    items: providers.filter((p) => p.provider === provider),
  })).filter((g) => g.items.length > 0);
}

/** The options a configuration's defaults can set: the driver's choices and switches. */
export function editableOptions(info: ProviderInfo): ConfigOption[] {
  return info.capabilities.config.filter((o) => o.type === 'select' || o.type === 'boolean');
}

/** The value a new session starts an option with under the configuration: its default, else the driver's. */
export function effectiveDefault(info: ProviderInfo, option: ConfigOption): ConfigSetting {
  return info.configuration.defaults[option.id] ?? option.currentValue;
}

/**
 * The defaults to save for the values the form holds: only what differs from the driver's own value, and the
 * entries the form cannot show stay as they are.
 */
export function defaultsToSave(
  info: ProviderInfo,
  values: Readonly<Record<string, ConfigSetting>>,
): SessionConfig {
  const editable = new Map(editableOptions(info).map((o) => [o.id, o]));
  const out: SessionConfig = {};
  for (const [id, value] of Object.entries(info.configuration.defaults)) {
    if (!editable.has(id)) out[id] = value;
  }
  for (const [id, option] of editable) {
    const value = values[id] ?? effectiveDefault(info, option);
    if (value !== option.currentValue) out[id] = value;
  }
  return out;
}

/** The entries of `providers` a new session or a handoff can use: the enabled ones. */
export function enabledConfigurations(providers: readonly ProviderInfo[]): ProviderInfo[] {
  return providers.filter((p) => p.configuration.enabled);
}

/**
 * Which configuration a handoff continues the session in on the target:
 * `builtin` when the vendor's own continues the vendor's own (no choice to show), `same` when the target has the
 * session's configuration enabled, `pick` when it has others that extend the same vendor, `disabled` when it has
 * configurations of the vendor and none is enabled (they are carried so the card can enable one), `none` when it
 * announced providers but none of this vendor. A target that announced no providers is not second-guessed
 * (`builtin`).
 */
export type HandoffChoice =
  | { state: 'builtin' }
  | { state: 'same'; options: ProviderInfo[]; value: string }
  | { state: 'pick'; options: ProviderInfo[] }
  | { state: 'disabled'; options: ProviderInfo[] }
  | { state: 'none' };

export function handoffChoice(
  target: readonly ProviderInfo[],
  vendor: Vendor,
  configuration: string,
): HandoffChoice {
  if (target.length === 0) return { state: 'builtin' };
  const options = enabledConfigurations(target).filter((p) => p.provider === vendor);
  const same = options.find((p) => p.configuration.id === configuration);
  if (same) {
    return configuration === vendor
      ? { state: 'builtin' }
      : { state: 'same', options, value: configuration };
  }
  if (options.length > 0) return { state: 'pick', options };
  const disabled = target.filter((p) => p.provider === vendor);
  return disabled.length > 0 ? { state: 'disabled', options: disabled } : { state: 'none' };
}
