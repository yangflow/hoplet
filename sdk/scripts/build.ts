// Builds dist/: one ESM file with @hoplet/shared inlined (valibot stays a dependency) and the declarations.
//   node scripts/build.ts
import { execFileSync } from 'node:child_process';
import { readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const dist = path.join(root, 'dist');
const typesDir = path.join(dist, 'types');

rmSync(dist, { recursive: true, force: true });

await build({
  entryPoints: [path.join(root, 'src/index.ts')],
  outfile: path.join(dist, 'index.js'),
  bundle: true,
  format: 'esm',
  platform: 'neutral',
  target: 'es2023',
  external: ['valibot'],
  logLevel: 'warning',
});

execFileSync('pnpm', ['exec', 'tsc', '-p', path.join(root, 'tsconfig.build.json')], {
  cwd: root,
  stdio: 'inherit',
});

// The declarations name `@hoplet/shared`, which is not published, and `.ts` files; point them at the emitted
// copy of shared/ and at the emitted `.d.ts` files.
function* declarations(dir: string): Generator<string> {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) yield* declarations(full);
    else if (entry.name.endsWith('.d.ts')) yield full;
  }
}
const sharedIndex = path.join(typesDir, 'shared/src/index.d.ts');
for (const file of declarations(typesDir)) {
  let toShared = path.relative(path.dirname(file), sharedIndex).split(path.sep).join('/');
  if (!toShared.startsWith('.')) toShared = `./${toShared}`;
  const source = readFileSync(file, 'utf8');
  const rewritten = source
    .replaceAll("'@hoplet/shared'", `'${toShared}'`)
    .replace(/(\bfrom\s+|\bimport\()(['"])(\.[^'"]*)\.ts\2/g, '$1$2$3.js$2');
  if (rewritten !== source) writeFileSync(file, rewritten);
}
