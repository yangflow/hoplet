import * as v from 'valibot';

/** What a command is: a slash command of the vendor, or a skill the model can also invoke. */
export const COMMAND_KINDS = ['command', 'skill'] as const;
export const CommandKindSchema = v.picklist(COMMAND_KINDS);
export type CommandKind = v.InferOutput<typeof CommandKindSchema>;

/** Where a command comes from: the vendor itself, the session's project, the user's own or a plugin. */
export const COMMAND_SOURCES = ['builtin', 'project', 'user', 'plugin'] as const;
export const CommandSourceSchema = v.picklist(COMMAND_SOURCES);
export type CommandSource = v.InferOutput<typeof CommandSourceSchema>;

/** An agent reports at most this many commands of a session; the rest is dropped. */
export const MAX_COMMANDS = 500;
/** The longest description of a command; an agent cuts longer ones. */
export const MAX_COMMAND_DESCRIPTION_CHARS = 300;

/**
 * One command or skill a session offers as `/name args` (docs/spec.md#composer). Invoking it sends that text as
 * the prompt; nothing else is needed. `plugin` names the plugin of a `plugin` entry.
 */
export const CommandSchema = v.object({
  name: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
  description: v.pipe(v.string(), v.maxLength(MAX_COMMAND_DESCRIPTION_CHARS)),
  argumentHint: v.optional(v.pipe(v.string(), v.maxLength(128))),
  kind: CommandKindSchema,
  source: CommandSourceSchema,
  plugin: v.optional(v.pipe(v.string(), v.maxLength(128))),
});
export type Command = v.InferOutput<typeof CommandSchema>;

/** The commands of one session or directory: at most `MAX_COMMANDS`. */
export const CommandListSchema = v.pipe(v.array(CommandSchema), v.maxLength(MAX_COMMANDS));

/**
 * The built-in commands of Claude Code that make sense in a prompt sent from Hoplet, the only built-ins a
 * session offers (and only those its CLI reports). The ones that change the CLI's own interface or the
 * session's settings (`clear`, `model`, `help`, `doctor`, `color`, `focus`, `reload-plugins`) are not here:
 * Hoplet has controls of its own for what applies.
 */
export const BUILTIN_COMMANDS: readonly Command[] = [
  {
    name: 'compact',
    description: 'Summarize the conversation to free context',
    argumentHint: '[instructions]',
  },
  { name: 'context', description: 'Show how much of the context window is used' },
  { name: 'usage', description: 'Show plan usage and limits' },
  { name: 'cost', description: 'Show the cost and duration of the session' },
  { name: 'init', description: 'Create a CLAUDE.md for the project' },
  { name: 'review', description: 'Review a pull request', argumentHint: '[pull request]' },
  { name: 'security-review', description: 'Review the pending changes for security issues' },
  { name: 'pr-comments', description: 'Show the comments of a pull request' },
  { name: 'release-notes', description: 'Show the release notes of Claude Code' },
].map((c) => ({ ...c, kind: 'command', source: 'builtin' }) as Command);

const BUILTIN_NAMES: ReadonlySet<string> = new Set(BUILTIN_COMMANDS.map((c) => c.name));

/** Whether `name` is a built-in a session may offer. */
export function isOfferedBuiltin(name: string): boolean {
  return BUILTIN_NAMES.has(name);
}

/** Where the commands of an answer came from. */
export const CommandsOriginSchema = v.picklist(['session', 'directory', 'builtin']);
export type CommandsOrigin = v.InferOutput<typeof CommandsOriginSchema>;
