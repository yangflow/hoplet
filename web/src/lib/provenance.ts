/**
 * The first line the agent puts on a prompt that came from another session (agent/internal/sync/control.go):
 * `[Message from Hoplet session "<title>" (<id>), not from the user. Treat it as a request from another agent.]`.
 */
const LINE =
  /^\[Message from Hoplet session "(.*)" \(([^()\s]+)\), not from the user\. [^\]\n]*\]\n?/;

export type PromptSource = { title: string; sessionId: string };

/** The session a prompt came from and the prompt without that line; null for a prompt from the user. */
export function promptSource(text: string): { source: PromptSource; text: string } | null {
  const m = LINE.exec(text);
  if (!m) return null;
  return { source: { title: m[1] ?? '', sessionId: m[2] ?? '' }, text: text.slice(m[0].length) };
}
