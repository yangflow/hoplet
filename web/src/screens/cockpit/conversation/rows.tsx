import { Fragment, type ReactNode } from 'react';
import { Tokens } from '../../../components/Code.tsx';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import { Markdown } from '../../../components/Markdown.tsx';
import { clock, formatBytes, formatDuration } from '../../../lib/format.ts';
import { t } from '../../../lib/i18n.ts';
import { type PromptSource, promptSource } from '../../../lib/provenance.ts';
import { StoredElided } from './blocks.tsx';
import { linesOf, OutputWell, type WellLine } from './OutputWell.tsx';
import { ToolRule } from './rule-line.tsx';
import {
  metaParts,
  numberedLines,
  type ToolFacts,
  type ToolRef,
  type ToolSpec,
  toolObject,
  toolRefOf,
  toolSpec,
  verbLabel,
  verbsOf,
} from './tools.ts';
import type {
  BoundaryRow,
  CommandRow,
  EditRow,
  FoldRow,
  NoteRow,
  Row,
  TextRow,
  ThinkingRow,
  ToolRow,
  Turn,
} from './turns.ts';
import { useView } from './view-context.tsx';

function plural(n: number, one: string, many: string): string {
  return t(n === 1 ? one : many).replace('{n}', String(n));
}

/** The terse summary of a finished turn: `2 files · 6 tools · 1m 08s`. */
export function summaryParts(summary: Turn['summary']): string[] {
  const parts: string[] = [];
  if (summary.files > 0) parts.push(plural(summary.files, '{n} file', '{n} files'));
  if (summary.tools > 0) parts.push(plural(summary.tools, '{n} tool', '{n} tools'));
  if (summary.durationMs !== null) parts.push(formatDuration(summary.durationMs));
  return parts;
}

export function Summary({ parts }: { parts: string[] }) {
  if (parts.length === 0) return null;
  return (
    <div className="sum" data-testid="cockpit.turn-summary">
      {parts.map((part, i) => (
        <Fragment key={part}>
          {i > 0 ? <i>·</i> : null}
          <span>{part}</span>
        </Fragment>
      ))}
    </div>
  );
}

/** Where a prompt came from when another session sent it (ADR 0034), in the meta row under the prompt. */
export function PromptSourceLine(props: {
  source: PromptSource;
  onOpen?: ((sessionId: string) => void) | undefined;
}) {
  const label = t('Open the session this prompt came from');
  return (
    <span className="pvl" data-testid="cockpit.prompt-source">
      {Icon.queue()}
      <span>{t('From session')}</span>
      <button
        type="button"
        className="lnk"
        title={label}
        aria-label={`${label}: ${props.source.title}`}
        onClick={() => props.onOpen?.(props.source.sessionId)}
      >
        {props.source.title}
      </button>
      {Icon.external()}
    </span>
  );
}

/** The header of a finished turn: the prompt, its time (the line range in the tooltip) and the summary. */
export function TurnHeader(props: {
  turn: Turn;
  stuck?: boolean;
  onOpenSession?: ((sessionId: string) => void) | undefined;
}) {
  const { turn, stuck, onOpenSession } = props;
  const from = turn.prompt && !turn.prompt.command ? promptSource(turn.prompt.text) : null;
  const range = t('Lines {a}–{b}')
    .replace('{a}', turn.startLine.toLocaleString())
    .replace('{b}', turn.endLine.toLocaleString());
  return (
    <header
      className={`th${stuck ? ' stuck' : ''}`}
      data-testid="cockpit.turn-head"
      data-turn={turn.key}
    >
      <div className={`tp${turn.prompt ? '' : ' none'}`}>
        {turn.prompt?.command ? (
          <span className="cmd-chip" data-testid="cockpit.command-chip">
            <span className="cn">/{turn.prompt.command.name}</span>
            {turn.prompt.command.args ? (
              <span className="ca">{turn.prompt.command.args}</span>
            ) : null}
          </span>
        ) : turn.prompt ? (
          (from?.text ?? turn.prompt.text)
        ) : (
          t('Earlier in this turn')
        )}
      </div>
      <Summary parts={summaryParts(turn.summary)} />
      <div className="meta">
        <span className="num" title={range}>
          {clock(turn.ts)}
        </span>
        {from ? <PromptSourceLine source={from.source} onOpen={onOpenSession} /> : null}
      </div>
    </header>
  );
}

/** The meta of a row: its parts in tabular type, separated by a dot. */
export function Meta({ parts }: { parts: ReactNode[] }) {
  return (
    <>
      {parts.map((part, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: the parts are fixed in order
        <Fragment key={i}>
          {i > 0 ? <i className="sep">·</i> : null}
          <span>{part}</span>
        </Fragment>
      ))}
    </>
  );
}

/** The four columns of a tool row: icon, past-tense verb, object, meta (the grid is the row's). */
export function ActCells(props: {
  icon: ReactNode;
  verb: string;
  object: ReactNode;
  meta?: ReactNode;
  dim?: boolean;
}) {
  return (
    <>
      <span className="i">{props.icon}</span>
      <span className="k" title={props.verb}>
        {props.verb}
      </span>
      <span className={`a${props.dim ? ' dim' : ''}`}>{props.object}</span>
      <span className="m">{props.meta}</span>
    </>
  );
}

function Mono({ children }: { children: ReactNode }) {
  return <span className="mono">{children}</span>;
}

/** The object of a call: code for what the model typed (paths, commands, queries), prose for MCP's server › tool. */
function ObjectView({ spec, arg }: { spec: ToolSpec; arg: string }) {
  if (spec.kind === 'call' && spec.mcp) {
    return (
      <Mono>
        {spec.mcp.server} <span className="d">›</span> {spec.mcp.tool}
      </Mono>
    );
  }
  if (spec.kind === 'other') return <Mono>{arg}</Mono>;
  return <Mono>{toolObject(spec, arg)}</Mono>;
}

function record(input: unknown): Record<string, unknown> {
  return input && typeof input === 'object' ? (input as Record<string, unknown>) : {};
}

/** A search hit `path:line:excerpt`, shown as `path:line  excerpt` with the match marked. */
function hitLine(line: string, pattern: string | undefined): WellLine {
  const m = /^(.+?):(\d+)[:-](.*)$/.exec(line);
  if (!m) return { text: line };
  const [, path = '', no = '', excerpt = ''] = m;
  let at = -1;
  let len = 0;
  if (pattern && pattern.length < 120) {
    try {
      const hit = new RegExp(pattern, 'i').exec(excerpt);
      if (hit && hit[0] !== '') {
        at = hit.index;
        len = hit[0].length;
      }
    } catch {
      /* a pattern that is not a regular expression marks nothing */
    }
  }
  return {
    text: `${path}:${no}  ${excerpt}`,
    node: (
      <>
        <span className="p">
          {path}:{no}
        </span>
        {'  '}
        {at < 0 ? (
          excerpt
        ) : (
          <>
            {excerpt.slice(0, at)}
            <span className="hl">{excerpt.slice(at, at + len)}</span>
            {excerpt.slice(at + len)}
          </>
        )}
      </>
    ),
  };
}

/** The lines a call's output well shows, by the kind of call; null when it has nothing to open. */
export function wellOf(
  spec: ToolSpec,
  facts: ToolFacts,
): { lines: WellLine[]; numbered: boolean } | null {
  const rec = record(facts.input);
  const text = facts.result?.text ?? '';
  switch (spec.kind) {
    case 'write': {
      const content = rec.content;
      if (typeof content !== 'string') return null;
      return { lines: linesOf(content), numbered: true };
    }
    case 'view':
      return null;
    case 'read': {
      const numbered = numberedLines(text);
      if (numbered)
        return { lines: numbered.map((l) => ({ text: l.text, no: l.no })), numbered: true };
      return { lines: linesOf(text), numbered: false };
    }
    case 'run': {
      const command = typeof rec.command === 'string' ? rec.command : '';
      const head: WellLine[] = command
        ? command
            .split('\n')
            .map((l, i) => ({ text: l, ...(i === 0 ? { kind: 'cmd' as const } : {}) }))
        : [];
      // the exit code is in the row's meta; the well keeps the output
      const output = text.replace(/^Exit code -?\d+\n?/, '');
      return {
        lines: [...head, ...(output === '' && text !== '' ? [] : linesOf(output))],
        numbered: false,
      };
    }
    case 'search': {
      const pattern = typeof rec.pattern === 'string' ? rec.pattern : undefined;
      const lines = linesOf(text).map((l) => hitLine(l.text, pattern));
      return { lines, numbered: false };
    }
    default:
      return { lines: linesOf(text), numbered: false };
  }
}

/** The running meta of a call: a spinner and a timer that ticks. */
export function RunningMeta({ seconds }: { seconds: number }) {
  return (
    <>
      <Spinner />
      <span>{t('{n} s').replace('{n}', String(seconds))}</span>
    </>
  );
}

/**
 * One tool call as a row and, opened, its output well. Stored and live calls share it: `facts` is what the call
 * and its result carry, `running` shows the spinner and `seconds` the timer. A failed call keeps its place with a
 * danger rule and shows its error; any other result opens on demand.
 */
export function ToolActivity(props: {
  id: string;
  facts: ToolFacts;
  arg: string;
  running?: boolean;
  seconds?: number;
  testid: string;
  /** The call's vendor ID: the rule that decided it is shown under the row. */
  toolUseId?: string | undefined;
  /** The elided block of a result, shown under the row in place of the well. */
  elided?: ReactNode;
}) {
  const { id, facts, arg, running = false, seconds = 0, elided } = props;
  const view = useView();
  const spec = toolSpec(facts);
  const failed = facts.result?.failed === true;
  const well = facts.result && !elided ? wellOf(spec, facts) : null;
  const open = well !== null && view.isOpen(id, failed);
  const parts = running ? [] : metaParts(facts);
  const cells = (
    <ActCells
      icon={Icon[spec.icon]()}
      verb={verbLabel(spec)}
      object={<ObjectView spec={spec} arg={arg} />}
      meta={running ? <RunningMeta seconds={seconds} /> : <Meta parts={parts} />}
    />
  );
  const cls = `act${running ? ' run' : ''}${failed ? ' bad' : ''}`;
  if (well === null) {
    return (
      <div data-testid={props.testid}>
        <div className={cls}>{cells}</div>
        {elided}
        <ToolRule toolUseId={props.toolUseId} />
      </div>
    );
  }
  return (
    <>
      <details
        className="act-d"
        open={open}
        onToggle={(e) => view.setOpen(id, e.currentTarget.open)}
        data-testid={props.testid}
      >
        <summary className={cls}>{cells}</summary>
        {open ? (
          <OutputWell
            lines={well.lines}
            numbered={well.numbered}
            error={failed}
            label={t('Output')}
          />
        ) : null}
      </details>
      <ToolRule toolUseId={props.toolUseId} />
    </>
  );
}

export function ToolView({ row }: { row: ToolRow }) {
  return (
    <ToolActivity
      id={row.key}
      facts={factsOf(row)}
      arg={row.arg}
      testid="cockpit.activity"
      toolUseId={row.toolUseId}
      {...(row.result?.elided ? { elided: <StoredElided elided={row.result.elided} /> } : {})}
    />
  );
}

/** What a stored call and its result carry: the lines' own times give the duration. */
export function factsOf(row: ToolRow | EditRow): ToolFacts {
  const startedAt = 'ts' in row && row.ts ? Date.parse(row.ts) : Number.NaN;
  const endedAt = row.result?.ts ? Date.parse(row.result.ts) : Number.NaN;
  return {
    ...toolRefOf(row),
    input: 'input' in row ? row.input : undefined,
    ...(row.result
      ? {
          result: {
            text: row.result.text,
            failed: row.result.failed,
            bytes: row.result.bytes,
            ...(row.result.elided ? { elided: true } : {}),
          },
        }
      : {}),
    ...(Number.isFinite(startedAt) ? { startedAt } : {}),
    ...(Number.isFinite(endedAt) ? { endedAt } : {}),
  };
}

/** The counts of an edit: green added lines, red removed ones. */
export function EditCounts({ added, removed }: { added: number; removed: number }) {
  return (
    <>
      {added > 0 ? <span className="add-n">+{added}</span> : null}
      {removed > 0 ? <span className="del-n">−{removed}</span> : null}
    </>
  );
}

/** The diff in the well's frame, gutter and type; its action opens the file's full diff in the inspector. */
export function DiffWell(props: {
  rows: EditRow['rows'];
  more: number;
  language: EditRow['language'];
  path: string;
}) {
  const view = useView();
  return (
    <div className="well" data-testid="cockpit.diff">
      <div className="wact">
        <IconButton
          label={t('Open the diff in the workspace')}
          icon={Icon.external()}
          onClick={() => view.openDiff?.(props.path)}
          data-testid="cockpit.open-diff"
        />
      </div>
      <div className="diff">
        {props.rows.map((line, i) => (
          <div
            // biome-ignore lint/suspicious/noArrayIndexKey: rows have no identity
            key={i}
            className={`dl${line.kind === 'add' ? ' add' : line.kind === 'del' ? ' del' : ''}`}
          >
            <span className="n">{line.oldNo ?? ''}</span>
            <span className="n">{line.newNo ?? ''}</span>
            <span className="s">{line.kind === 'add' ? '+' : line.kind === 'del' ? '−' : ' '}</span>
            <span className="c">
              <Tokens line={line.text} language={props.language} />
            </span>
          </div>
        ))}
        {props.more > 0 ? (
          <div className="dl more">
            <span className="n" />
            <span className="n" />
            <span className="s" />
            <span className="c">{t('{n} more lines').replace('{n}', String(props.more))}</span>
          </div>
        ) : null}
      </div>
    </div>
  );
}

/** An edit: its row with the counts and the diff under it; the diff is open unless it is folded away. */
export function EditActivity(props: {
  id: string;
  tool: ToolRef;
  path: string;
  added: number;
  removed: number;
  rows: EditRow['rows'];
  more: number;
  language: EditRow['language'];
  failed: boolean;
  /** The diff is open until the reader closes it. */
  defaultOpen: boolean;
  running?: boolean;
  seconds?: number;
  testid: string;
  /** The call's vendor ID: the rule that decided it is shown under the row. */
  toolUseId?: string | undefined;
}) {
  const view = useView();
  const spec = toolSpec(props.tool);
  const open = view.isOpen(props.id, props.defaultOpen);
  return (
    <>
      <details
        className="act-d"
        open={open}
        onToggle={(e) => view.setOpen(props.id, e.currentTarget.open)}
        data-testid={props.testid}
      >
        <summary className={`act${props.running ? ' run' : ''}${props.failed ? ' bad' : ''}`}>
          <ActCells
            icon={Icon[spec.icon]()}
            verb={verbLabel(spec)}
            object={<Mono>{props.path}</Mono>}
            meta={
              props.running ? (
                <RunningMeta seconds={props.seconds ?? 0} />
              ) : (
                <>
                  {props.failed ? <span>{t('Failed')}</span> : null}
                  <EditCounts added={props.added} removed={props.removed} />
                </>
              )
            }
          />
        </summary>
        {open ? (
          <DiffWell
            rows={props.rows}
            more={props.more}
            language={props.language}
            path={props.path}
          />
        ) : null}
      </details>
      <ToolRule toolUseId={props.toolUseId} />
    </>
  );
}

export function EditView({ row }: { row: EditRow }) {
  return (
    <EditActivity
      id={row.key}
      tool={toolRefOf(row)}
      path={row.path}
      added={row.added}
      removed={row.removed}
      rows={row.rows}
      more={row.more}
      language={row.language}
      failed={row.result?.failed === true}
      defaultOpen
      testid="cockpit.edit"
      toolUseId={row.toolUseId}
    />
  );
}

export function FoldView({ row }: { row: FoldRow }) {
  const view = useView();
  return (
    <details
      className="act-d fold"
      open={view.isOpen(row.key)}
      onToggle={(e) => view.setOpen(row.key, e.currentTarget.open)}
      data-testid="cockpit.tools"
    >
      <summary className="act">
        <ActCells
          icon={Icon.tools()}
          verb={plural(row.tools.length, '{n} tool', '{n} tools')}
          dim
          object={<span className="d">{verbsOf(row.tools.map(toolRefOf)).join(', ')}</span>}
          meta={
            <span className={`chv${view.isOpen(row.key) ? ' open' : ''}`}>{Icon.chevron()}</span>
          }
        />
      </summary>
      {row.tools.map((tool) => (
        <ToolView key={tool.key} row={tool} />
      ))}
    </details>
  );
}

/** The first line of a text, cut to what a row can name. */
function firstLine(text: string): string {
  return (text.trim().split('\n')[0] ?? '').slice(0, 160);
}

/** A thought: one row of its first line; opened, the whole text in dim italic UI type. */
export function ThoughtActivity(props: {
  id: string;
  text: string;
  meta?: ReactNode;
  testid: string;
  elided?: ReactNode;
}) {
  const view = useView();
  const first = firstLine(props.text);
  const more = props.text.trim() !== first;
  const cells = (
    <ActCells icon={Icon.thinking()} verb={t('Thought')} object={first} meta={props.meta} dim />
  );
  if (props.elided || !more) {
    return (
      <div data-testid={props.testid}>
        <div className="act think">{cells}</div>
        {props.elided}
      </div>
    );
  }
  return (
    <details
      className="act-d"
      open={view.isOpen(props.id)}
      onToggle={(e) => view.setOpen(props.id, e.currentTarget.open)}
      data-testid={props.testid}
    >
      <summary className="act think">{cells}</summary>
      <p className="thought">{props.text.trim()}</p>
    </details>
  );
}

function ThinkingView({ row }: { row: ThinkingRow }) {
  return (
    <ThoughtActivity
      id={row.key}
      text={row.text}
      testid="cockpit.thinking"
      {...(row.elided ? { elided: <StoredElided elided={row.elided} /> } : {})}
    />
  );
}

function noteIcon(family: NoteRow['family']): ReactNode {
  return family === 'file' ? Icon.file() : Icon.info();
}

/** A result without its call, a system line or an attachment: a row naming it, its text opened on demand. */
export function NoteActivity(props: {
  id: string;
  family: NoteRow['family'];
  label: string;
  text: string;
  bytes: number;
  failed?: boolean | undefined;
  testid: string;
  elided?: ReactNode;
}) {
  const view = useView();
  const first = firstLine(props.text);
  const more = props.text.trim() !== first;
  const cells = (
    <ActCells
      icon={noteIcon(props.family)}
      verb={props.label}
      object={<span className={props.family === 'result' ? 'mono' : ''}>{first}</span>}
      meta={
        <Meta
          parts={[
            ...(props.failed ? [t('Failed')] : []),
            ...(props.bytes > 0 ? [formatBytes(props.bytes)] : []),
          ]}
        />
      }
    />
  );
  const cls = `act${props.failed ? ' bad' : ''}`;
  if (props.elided || !more) {
    return (
      <div data-testid={props.testid}>
        <div className={cls}>{cells}</div>
        {props.elided}
      </div>
    );
  }
  const open = view.isOpen(props.id, props.failed === true);
  return (
    <details
      className="act-d"
      open={open}
      onToggle={(e) => view.setOpen(props.id, e.currentTarget.open)}
      data-testid={props.testid}
    >
      <summary className={cls}>{cells}</summary>
      {open ? (
        <OutputWell lines={linesOf(props.text)} error={props.failed === true} label={t('Output')} />
      ) : null}
    </details>
  );
}

function NoteView({ row }: { row: NoteRow }) {
  return (
    <NoteActivity
      id={row.key}
      family={row.family}
      label={row.family === 'result' ? t('Result') : row.label}
      text={row.text}
      bytes={row.elided ? row.elided.bytes : row.text.length}
      failed={row.failed}
      testid="cockpit.note"
      {...(row.elided ? { elided: <StoredElided elided={row.elided} /> } : {})}
    />
  );
}

function TextView({ row }: { row: TextRow }) {
  return (
    <div data-testid="cockpit.text">
      <div className="text">
        <Markdown text={row.text} />
      </div>
      {row.elided ? <StoredElided elided={row.elided} solo /> : null}
    </div>
  );
}

/** What a local command printed, as a quiet block under the prompt that ran it. */
function CommandView({ row }: { row: CommandRow }) {
  return (
    <div className="cmd-out" data-testid="cockpit.command-output">
      <div className="coh meta">
        {Icon.terminal()}
        <span>{t('Command output')}</span>
        {row.command ? <span className="mono">/{row.command}</span> : null}
      </div>
      {row.compact ? (
        <div className="cob ok">
          {Icon.check()}
          {t('Compacted')}
          {row.compact.from && row.compact.to ? (
            <span className="meta">
              {t('{from} tokens summarised to {to}')
                .replace('{from}', row.compact.from)
                .replace('{to}', row.compact.to)}
            </span>
          ) : null}
        </div>
      ) : (
        <div className="cob">
          <Markdown text={row.text} />
        </div>
      )}
    </div>
  );
}

/** Where the conversation was compacted. */
function BoundaryView({ row }: { row: BoundaryRow }) {
  const label = t('Conversation compacted');
  return (
    // biome-ignore lint/a11y/useSemanticElements: a labelled separator is the divider's role; <hr> cannot hold the label
    <div
      className="cmd-bound meta"
      role="separator"
      aria-label={label}
      data-testid="cockpit.compact-boundary"
    >
      {label}
      {row.sizes ? (
        <span>
          {t('{from} tokens summarised to {to}')
            .replace('{from}', row.sizes.from)
            .replace('{to}', row.sizes.to)}
        </span>
      ) : null}
    </div>
  );
}

/** A steered message: the row sits at the point of the turn where the model took it in. */
export function SteerActivity(props: {
  text: string;
  /** Sent, and not yet taken in: it shows as "Steering" at the foot of the live turn. */
  pending?: boolean;
  elided?: ReactNode;
  testid?: string;
}) {
  return (
    <div data-testid={props.testid ?? 'cockpit.steer'}>
      <div className={`act steer${props.pending ? ' pend' : ''}`}>
        <ActCells
          icon={props.pending ? <Spinner /> : Icon.steer()}
          verb={props.pending ? t('Steering') : t('Steered')}
          object={props.text}
          meta={props.pending ? <span className="meta">{t('at the next tool call')}</span> : null}
        />
      </div>
      {props.elided}
    </div>
  );
}

/** One row of a turn's body. */
export function RowView({ row }: { row: Row }) {
  switch (row.kind) {
    case 'text':
      return <TextView row={row} />;
    case 'thinking':
      return <ThinkingView row={row} />;
    case 'tool':
      return <ToolView row={row} />;
    case 'edit':
      return <EditView row={row} />;
    case 'note':
      return <NoteView row={row} />;
    case 'command':
      return <CommandView row={row} />;
    case 'boundary':
      return <BoundaryView row={row} />;
    case 'steer':
      return (
        <SteerActivity
          text={row.text}
          {...(row.elided ? { elided: <StoredElided elided={row.elided} /> } : {})}
        />
      );
    case 'fold':
      return <FoldView row={row} />;
  }
}
