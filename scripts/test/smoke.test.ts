import { spawn, spawnSync } from 'node:child_process';
import {
  closeSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  openSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { expect, test } from 'vitest';

// Run the harness with inert processes; it must never reach a real agent or saved state.
function harness(failMint: boolean, rejectListen = false) {
  const root = mkdtempSync(join(tmpdir(), 'hoplet-smoke-test-'));
  const bin = join(root, 'bin');
  mkdirSync(bin);
  mkdirSync(join(root, 'scripts'));
  mkdirSync(join(root, 'agent'));
  mkdirSync(join(root, 'artifacts'));
  copyFileSync(resolve('scripts/smoke.sh'), join(root, 'scripts/smoke.sh'));
  const script = (name: string, body: string) =>
    writeFileSync(join(bin, name), `#!/bin/bash\nset -eu\n${body}\n`, { mode: 0o755 });
  script('mktemp', 'echo "$TEST_ROOT/artifacts"');
  script('sqlite3', 'cat >/dev/null');
  script(
    'go',
    `echo "$*" > "$TEST_ROOT/build-args"
cat > "$TEST_ROOT/artifacts/hoplet-agent" <<'AGENT'
#!/bin/bash
echo $$ > "$TEST_ROOT/agent-pid"
exec "$REAL_NODE" -e 'setInterval(() => {}, 1000)'
AGENT
chmod +x "$TEST_ROOT/artifacts/hoplet-agent"`,
  );
  script(
    'node',
    `if [[ "$1" == core/src/main.ts ]]; then
  echo $$ > "$TEST_ROOT/server-pid"
  ${rejectListen ? 'exit 1' : `echo '{"message":"listening"}'`}
  exec "$REAL_NODE" -e 'setInterval(() => {}, 1000)'
elif [[ "$1" == scripts/gen-fixture.mjs || "$1" == scripts/gen-codex-fixture.mjs ]]; then
  echo '{}' > "$2"
else
  exec "$REAL_NODE" "$@"
fi`,
  );
  script(
    'curl',
    `echo called > "$TEST_ROOT/api-called"
case "$*" in
  */api/health*) echo '{}' ;;
  */api/devices*) ${failMint ? 'exit 1' : `echo '{"token":"synthetic"}'`} ;;
  *) echo '{"sessions":[]}' ;;
esac`,
  );
  // Legacy binary is deliberately unusable, even if present.
  writeFileSync(join(root, 'agent/hoplet-agent'), '#!/bin/bash\nexit 99\n', { mode: 0o755 });
  const out = openSync(join(root, 'smoke.out'), 'a');
  const child = spawn('bash', ['scripts/smoke.sh'], {
    cwd: root,
    env: {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      TEST_ROOT: root,
      REAL_NODE: process.execPath,
    },
    // The script's output goes to a file, so a run that dies early says why.
    stdio: ['ignore', out, out],
    // Its own process group, so a test can send SIGINT the way a terminal's Ctrl-C does.
    detached: true,
  });
  closeSync(out);
  const exited = new Promise<number | null>((done) => child.on('exit', done));
  return { root, child, exited, output: () => readFileSync(join(root, 'smoke.out'), 'utf8') };
}

/** The harness generates a fixture session before it starts the agent, which can take seconds on a busy machine. */
const START_TIMEOUT_MS = 30_000;

type Harness = { exited: Promise<number | null>; output(): string };

/**
 * Waits until the script is past `AGENT_PID=$!`, which is when its trap can reap the agent: the fake agent writes
 * its pid file the moment it starts, possibly before bash has recorded the pid, and a signal in that gap leaves an
 * agent that ignores SIGINT (a background job of a non-interactive shell) running. The script announces the next
 * step right after. A script that exits first never gets there, so that ends the wait at once with its output
 * instead of leaving the test to its timeout.
 */
async function waitForAgent(root: string, h: Harness) {
  let exit: number | null | undefined;
  void h.exited.then((code) => {
    exit = code;
  });
  const deadline = Date.now() + START_TIMEOUT_MS;
  while (Date.now() < deadline) {
    if (exit !== undefined) {
      throw new Error(`The script exited (${exit}) before it started the agent:\n${h.output()}`);
    }
    if (existsSync(join(root, 'agent-pid')) && h.output().includes(AGENT_STARTED)) return;
    await new Promise((r) => setTimeout(r, 20));
  }
  throw new Error(`The agent did not start:\n${h.output()}`);
}

/** The step the script announces right after it records the agent's pid. */
const AGENT_STARTED = '③';

/** The script's exit code, or an error carrying its output when it has not ended in time. */
async function exitOf(h: Harness, ms = 15_000) {
  let timer: NodeJS.Timeout | undefined;
  const late = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`The script did not exit:\n${h.output()}`)), ms);
  });
  try {
    return await Promise.race([h.exited, late]);
  } finally {
    clearTimeout(timer);
  }
}

function assertReaped(root: string, name: string) {
  const pid = Number(readFileSync(join(root, `${name}-pid`), 'utf8'));
  expect(() => process.kill(pid, 0)).toThrow();
}

for (const signal of ['SIGINT', 'SIGTERM'] as const) {
  test(
    `smoke reaps current-source processes on ${signal}`,
    async () => {
      const h = harness(false);
      try {
        await waitForAgent(h.root, h);
        // Ctrl-C signals the whole foreground group; a SIGINT to bash alone is dropped while it waits on a
        // foreground command that does not die of it. kill(1) signals the script alone.
        process.kill(
          signal === 'SIGINT' ? -(h.child.pid as number) : (h.child.pid as number),
          signal,
        );
        expect(await exitOf(h)).not.toBe(0);
        expect(readFileSync(join(h.root, 'build-args'), 'utf8')).toContain('build -o');
        assertReaped(h.root, 'server');
        assertReaped(h.root, 'agent');
      } finally {
        h.child.kill('SIGKILL');
        for (const name of ['server', 'agent']) {
          try {
            process.kill(Number(readFileSync(join(h.root, `${name}-pid`), 'utf8')), 'SIGKILL');
          } catch {
            /* Already reaped. */
          }
        }
        rmSync(h.root, { recursive: true, force: true });
      }
    },
    START_TIMEOUT_MS + 10_000,
  );
}

test('smoke reaps server after setup fails', async () => {
  const h = harness(true);
  try {
    expect(await exitOf(h)).not.toBe(0);
    assertReaped(h.root, 'server');
    expect(readFileSync(join(h.root, 'build-args'), 'utf8')).toContain('build -o');
  } finally {
    h.child.kill('SIGKILL');
    try {
      process.kill(Number(readFileSync(join(h.root, 'server-pid'), 'utf8')), 'SIGKILL');
    } catch {
      /* Already reaped. */
    }
    rmSync(h.root, { recursive: true, force: true });
  }
});

test('smoke script parses in bash', () => {
  expect(spawnSync('bash', ['-n', 'scripts/smoke.sh']).status).toBe(0);
});

test('smoke refuses API calls when its server cannot listen', async () => {
  const h = harness(true, true);
  try {
    expect(await exitOf(h)).not.toBe(0);
    expect(() => readFileSync(join(h.root, 'api-called'))).toThrow();
    assertReaped(h.root, 'server');
  } finally {
    h.child.kill('SIGKILL');
    rmSync(h.root, { recursive: true, force: true });
  }
});
