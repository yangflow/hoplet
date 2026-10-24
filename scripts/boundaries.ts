// The import boundaries of docs/architecture.md#boundaries, checked over the source tree (TypeScript and Go).
//   node scripts/boundaries.ts        prints every violation; exit status 1 when there is one
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';

export type Violation = { file: string; specifier: string; rule: string };

const IMPORT =
  /\b(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\sfrom\s+)?["']([^"']+)["']|\bimport\(\s*["']([^"']+)["']\s*\)/g;

function specifiers(source: string): string[] {
  const found: string[] = [];
  for (const match of source.matchAll(IMPORT)) {
    const specifier = match[1] ?? match[2];
    if (specifier) found.push(specifier);
  }
  return found;
}

const GO_IMPORT_BLOCK = /^import\s*\(([^)]*)\)/gm;
const GO_IMPORT_LINE = /^import\s+(?:[\w.]+\s+)?"([^"]+)"/gm;
const GO_SPEC = /^\s*(?:[\w.]+\s+)?"([^"]+)"/gm;

function goImports(source: string): string[] {
  const found: string[] = [];
  for (const block of source.matchAll(GO_IMPORT_BLOCK))
    for (const spec of (block[1] ?? '').matchAll(GO_SPEC)) if (spec[1]) found.push(spec[1]);
  for (const line of source.matchAll(GO_IMPORT_LINE)) if (line[1]) found.push(line[1]);
  return found;
}

// A vendor package of the agent (ADR 0014): agent/internal/{provider,runtime}/<vendor>. Only the same
// vendor's packages and the daemon, which constructs them, import one; tests may.
const AGENT_INTERNAL = 'github.com/yangflow/hoplet/agent/internal/';
const VENDOR_PACKAGE = /^(?:provider|runtime)\/([^/]+)$/;
const NOT_VENDORS = new Set(['runtimetest', 'fakeclaude']);

function vendorOf(pkg: string): string | undefined {
  const vendor = VENDOR_PACKAGE.exec(pkg)?.[1];
  return vendor && !NOT_VENDORS.has(vendor) ? vendor : undefined;
}

function checkGo(
  file: string,
  source: string,
  add: (specifier: string, rule: string) => void,
): void {
  if (file.endsWith('_test.go') || !file.startsWith('agent/internal/')) return;
  const from = file.slice('agent/internal/'.length);
  if (from.startsWith('daemon/')) return;
  const own = vendorOf(path.posix.dirname(from));
  for (const specifier of goImports(source)) {
    if (!specifier.startsWith(AGENT_INTERNAL)) continue;
    const vendor = vendorOf(specifier.slice(AGENT_INTERNAL.length));
    if (vendor && vendor !== own)
      add(
        specifier,
        'a vendor package is imported only by its own vendor and by daemon (ADR 0014)',
      );
  }
}

// Where an import points, as a repository path ("core/src/store/index.ts"), or the package name when
// it is not relative.
function target(file: string, specifier: string): string {
  if (!specifier.startsWith('.')) return specifier;
  return path.posix.normalize(path.posix.join(path.posix.dirname(file), specifier));
}

// A module is core/src/<module>/ (docs/spec.md#modules)
const moduleOf = (repoPath: string): string | undefined =>
  /^core\/src\/([^/]+)\//.exec(repoPath)?.[1];

// Only `store` composes paths in the data directory, so only it (plus `config` and the static file
// server) may import the filesystem.
const FILESYSTEM = /^(?:node:)?fs(?:\/promises)?$/;
const FILESYSTEM_ALLOWED = /^core\/src\/(?:store\/|config\/|api\/static\.ts$)/;

// The one module that imports the icon family: the rest of the web UI takes icons from it.
const ICON_MODULE = 'web/src/components/icons.tsx';

// A screen is web/src/screens/<screen>/. Another screen is used through its index.tsx; the fixtures a screen
// builds for /__states are the only files that reach further.
const screenOf = (repoPath: string): string | undefined =>
  /^web\/src\/screens\/([^/]+)\//.exec(repoPath)?.[1];
const SCREEN_FIXTURE = /\/(?:states\.tsx|fixtures\.ts)$/;

// The SDK runs in Node, a browser and Bun alike: it imports its own files and the contract in shared/ (as
// `@hoplet/shared` or by path), never node:*, the core, the web UI or the agent.
const SDK_IMPORTS = /^(?:sdk\/|shared\/|@hoplet\/shared(?:\/|$))/;
const isTest = (file: string): boolean => /^sdk\/test\//.test(file);
// sdk/scripts/ is the build, which runs in Node.
const isSdkRuntime = (file: string): boolean =>
  file.startsWith('sdk/') && !file.startsWith('sdk/scripts/');

// The command line tool is built on the SDK and Node alone (ADR 0033): never on shared/, the core, the web UI
// or the agent, and it imports the SDK by its package name. Only scripts/ and one core test (the independent
// check of the offline audit verification) import it.
const CLI_FORBIDDEN =
  /^(?:core\/|web\/|shared\/|agent\/|sdk\/|scripts\/|@hoplet\/(?:shared|core|web)(?:\/|$))/;
const CLI_IMPORTERS = /^(?:cli\/|scripts\/|core\/test\/audit\.test\.ts$)/;

// Which module may import which, the "May call" column of docs/architecture.md#components. `config` is
// also read for its type where a module is handed the configuration.
const MAY_CALL: Record<string, readonly string[]> = {
  config: ['log'],
  log: ['scrub'],
  scrub: [],
  migrations: [],
  store: ['migrations', 'log', 'scrub'],
  control: ['store', 'log', 'scrub'],
  ingest: ['store', 'control', 'log', 'config'],
  notify: ['store', 'control', 'log', 'scrub'],
  hooks: ['store', 'control', 'notify', 'log', 'scrub'],
  api: ['store', 'control', 'notify', 'hooks', 'log', 'config', 'scrub'],
};

export function checkBoundaries(files: ReadonlyMap<string, string>): Violation[] {
  const violations: Violation[] = [];
  for (const [file, source] of files) {
    if (file.endsWith('.go')) {
      checkGo(file, source, (specifier, rule) => violations.push({ file, specifier, rule }));
      continue;
    }
    if (!file.endsWith('.ts') && !file.endsWith('.tsx')) continue;
    for (const specifier of specifiers(source)) {
      const to = target(file, specifier);
      const add = (rule: string) => violations.push({ file, specifier, rule });
      if (
        file.startsWith('shared/') &&
        (to.startsWith('core/') ||
          to.startsWith('web/') ||
          to.startsWith('@hoplet/core') ||
          to.startsWith('@hoplet/web'))
      )
        add('shared/ depends on nothing else in the repository');
      if (isSdkRuntime(file) && !SDK_IMPORTS.test(to) && !(isTest(file) && to === 'vitest'))
        add('sdk/ imports only shared/ and itself, and no node:* module (ADR 0033)');
      if (
        !file.startsWith('sdk/') &&
        !file.startsWith('cli/') &&
        !file.startsWith('scripts/') &&
        (to.startsWith('sdk/') || to.startsWith('@hoplet/sdk'))
      )
        add('only the command line tool imports the SDK (ADR 0033)');
      if (file.startsWith('cli/') && CLI_FORBIDDEN.test(to))
        add('cli/ imports only the SDK (@hoplet/sdk), node:* and itself (ADR 0033)');
      if (!CLI_IMPORTERS.test(file) && (to.startsWith('cli/') || to.startsWith('@hoplet/cli')))
        add('nothing but cli/ and scripts/ imports the command line tool');
      if (/^lucide-react(\/|$)/.test(to) && !file.startsWith('scripts/') && file !== ICON_MODULE)
        add(`icons are imported only in ${ICON_MODULE} (docs/design.md#icons)`);
      if (file.startsWith('web/') && (to.startsWith('core/') || to.startsWith('@hoplet/core')))
        add('the web UI talks to the core only through the API in shared/');
      if (file.startsWith('core/') && (to.startsWith('web/') || to.startsWith('@hoplet/web')))
        add('the core never imports from the web UI');
      if (FILESYSTEM.test(to) && file.startsWith('core/src/') && !FILESYSTEM_ALLOWED.test(file))
        add('only store, config and api/static touch the filesystem (node:fs)');
      const from = moduleOf(file);
      const into = moduleOf(to);
      if (from && into && from !== into && to !== `core/src/${into}/index.ts`)
        add(`another module is imported only through core/src/${into}/index.ts`);
      const screen = screenOf(file);
      const otherScreen = screenOf(to);
      if (
        screen &&
        otherScreen &&
        screen !== otherScreen &&
        to !== `web/src/screens/${otherScreen}/index.tsx` &&
        !SCREEN_FIXTURE.test(file) &&
        !SCREEN_FIXTURE.test(to)
      )
        add(
          'a screen uses another screen only through its index.tsx; shared code lives in lib/ or components/',
        );
      const allowed = from ? MAY_CALL[from] : undefined;
      if (from && into && from !== into && allowed && !allowed.includes(into))
        add(`${from} may not call ${into} (docs/architecture.md#components)`);
    }
  }
  return violations;
}

function trackedSources(root: string): Map<string, string> {
  const out = execFileSync(
    'git',
    ['-C', root, 'ls-files', 'core', 'shared', 'web', 'sdk', 'cli', 'scripts', 'agent'],
    { encoding: 'utf8' },
  );
  const files = new Map<string, string>();
  for (const file of out.split('\n').filter((f) => /\.(?:tsx?|go)$/.test(f)))
    files.set(file, readFileSync(path.join(root, file), 'utf8'));
  return files;
}

if (import.meta.main) {
  const violations = checkBoundaries(trackedSources(process.cwd()));
  for (const v of violations) console.log(`${v.file}: imports ${v.specifier}: ${v.rule}`);
  process.exit(violations.length ? 1 : 0);
}

export { trackedSources };
