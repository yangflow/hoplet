// Builds dist/hoplet.js: the command as one ESM file. @hoplet/sdk stays a dependency, so the package and the
// SDK it needs are installed at the same version.
//   node scripts/build.ts
import { chmodSync, rmSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const dist = path.join(root, 'dist');
const out = path.join(dist, 'hoplet.js');

rmSync(dist, { recursive: true, force: true });

await build({
  entryPoints: [path.join(root, 'src/main.ts')],
  outfile: out,
  bundle: true,
  format: 'esm',
  platform: 'node',
  target: 'node22',
  external: ['@hoplet/sdk'],
  logLevel: 'warning',
});
chmodSync(out, 0o755);
