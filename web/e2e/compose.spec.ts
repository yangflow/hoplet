import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { freePort, HOST_TOKEN, root, run, until } from './stack.ts';

// docs/spec.md#acceptance-checklist: `docker compose up -d` with the compose file of the repository serves
// the web UI on 127.0.0.1 and the health check passes. The file is used as it is; a second file only moves
// the port and names the image the run built, so the run cannot meet a core on 7788.
const project = `hoplet-e2e-compose-${process.env.E2E_RUN ?? process.pid}`;
const dir = mkdtempSync(join(tmpdir(), 'hoplet-e2e-compose.'));
let compose: string[] = [];

const docker = (args: string[]) => run('docker', [...compose, ...args]);

test.afterAll(() => {
  try {
    run('docker', [...compose, 'down', '-v', '--remove-orphans']);
  } catch {}
  rmSync(dir, { recursive: true, force: true });
});

test('docker compose up -d serves the web UI on 127.0.0.1 and the health check passes', async () => {
  test.setTimeout(5 * 60_000);
  const port = await freePort();
  const override = join(dir, 'override.yaml');
  writeFileSync(
    override,
    [
      'services:',
      '  hoplet:',
      `    image: ${process.env.E2E_IMAGE_REF}`,
      '    ports: !override',
      `      - "127.0.0.1:${port}:7788"`,
      '',
    ].join('\n'),
  );
  compose = ['compose', '-p', project, '-f', join(root, 'image', 'compose.yaml'), '-f', override];
  process.env.HOPLET_TOKEN = HOST_TOKEN;
  docker(['up', '-d']);
  const id = docker(['ps', '-q', 'hoplet']).trim();
  expect(id).not.toBe('');

  // The published port is bound to the loopback interface only.
  const bindings = JSON.parse(
    run('docker', ['inspect', '--format', '{{json .NetworkSettings.Ports}}', id]),
  ) as Record<string, { HostIp: string; HostPort: string }[]>;
  expect(bindings['7788/tcp']).toEqual([{ HostIp: '127.0.0.1', HostPort: String(port) }]);

  // The image's health check passes (it first runs after its interval).
  const health = () =>
    run('docker', ['inspect', '--format', '{{.State.Health.Status}}', id]).trim();
  await until('the container to become healthy', 150_000, async () => health() === 'healthy', 2000);
  expect(health()).toBe('healthy');

  // The web UI is served, and the API answers to the host token.
  const page = await fetch(`http://127.0.0.1:${port}/`);
  expect(page.status).toBe(200);
  expect(page.headers.get('content-type')).toContain('text/html');
  expect(await page.text()).toContain('<div id="root"');
  const api = await fetch(`http://127.0.0.1:${port}/api/health`, {
    headers: { Authorization: `Bearer ${HOST_TOKEN}` },
  });
  expect(api.status).toBe(200);
  const refused = await fetch(`http://127.0.0.1:${port}/api/health`);
  expect(refused.status).toBe(401);

  // `down -v` leaves neither a container nor the data volume.
  docker(['down', '-v', '--remove-orphans']);
  expect(
    run('docker', ['ps', '-aq', '--filter', `label=com.docker.compose.project=${project}`]),
  ).toBe('');
  expect(
    run('docker', [
      'volume',
      'ls',
      '-q',
      '--filter',
      `label=com.docker.compose.project=${project}`,
    ]),
  ).toBe('');
});
