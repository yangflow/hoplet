import { spawn, spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
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
  const child = spawn('bash', ['scripts/smoke.sh'], {
    cwd: root,
    env: {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      TEST_ROOT: root,
      REAL_NODE: process.execPath,
    },
    stdio: 'ignore',
  });
  const exited = new Promise<number | null>((done) => child.on('exit', done));
  return { root, child, exited };
}

/** The harness generates a fixture session before it starts the agent, which can take seconds on a busy machine. */
const START_TIMEOUT_MS = 30_000;

async function waitForFile(path: string) {
  for (let i = 0; i < START_TIMEOUT_MS / 20; i++) {
    try {
      return readFileSync(path, 'utf8');
    } catch {
      await new Promise((r) => setTimeout(r, 20));
    }
  }
  throw new Error(`Process did not start: ${path}`);
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
        await waitForFile(join(h.root, 'agent-pid'));
        h.child.kill(signal);
        expect(await h.exited).not.toBe(0);
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
    expect(await h.exited).not.toBe(0);
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
    expect(await h.exited).not.toBe(0);
    expect(() => readFileSync(join(h.root, 'api-called'))).toThrow();
    assertReaped(h.root, 'server');
  } finally {
    h.child.kill('SIGKILL');
    rmSync(h.root, { recursive: true, force: true });
  }
});
