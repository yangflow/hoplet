import { describe, expect, it } from 'vitest';
import { checkBoundaries } from '../boundaries.ts';

const files = (entries: Record<string, string>): Map<string, string> =>
  new Map(Object.entries(entries));

describe('checkBoundaries', () => {
  it('allows shared, web and core to meet at the protocol', () => {
    const violations = checkBoundaries(
      files({
        'shared/src/index.ts': 'export interface SessionMeta { sessionId: string }',
        'core/src/store/index.ts': 'import type { SessionMeta } from "@hoplet/shared";',
        'web/src/screens/cockpit/index.tsx': 'import type { SessionMeta } from "@hoplet/shared";',
      }),
    );
    expect(violations).toEqual([]);
  });

  it('rejects web reaching into the core', () => {
    const violations = checkBoundaries(
      files({ 'web/src/lib/api.ts': 'import { Store } from "../../../core/src/store/index.ts";' }),
    );
    expect(violations).toHaveLength(1);
    expect(violations[0]?.rule).toContain('only through the API in shared/');
  });

  it('allows the icon family in the icon module only', () => {
    const ok = checkBoundaries(
      files({ 'web/src/components/icons.tsx': 'import { Search } from "lucide-react";' }),
    );
    expect(ok).toEqual([]);
    const bad = checkBoundaries(
      files({
        'web/src/screens/cockpit/conversation/rows.tsx': 'import { Search } from "lucide-react";',
        'web/src/components/file-types.tsx':
          'import Search from "lucide-react/dist/esm/icons/search";',
      }),
    );
    expect(bad.map((v) => v.file)).toEqual([
      'web/src/screens/cockpit/conversation/rows.tsx',
      'web/src/components/file-types.tsx',
    ]);
    expect(bad[0]?.rule).toContain('web/src/components/icons.tsx');
  });

  it('rejects shared depending on core or web', () => {
    const violations = checkBoundaries(
      files({ 'shared/src/index.ts': 'import { Store } from "@hoplet/core";' }),
    );
    expect(violations[0]?.rule).toBe('shared/ depends on nothing else in the repository');
  });

  it('rejects cross-module imports that skip the module index', () => {
    const violations = checkBoundaries(
      files({
        'core/src/ingest/index.ts': 'import { NotOwnerError } from "../store/store.ts";',
      }),
    );
    expect(violations[0]?.rule).toContain('only through core/src/store/index.ts');
  });

  it('allows importing another module through its index', () => {
    const violations = checkBoundaries(
      files({
        'core/src/ingest/index.ts': 'import { Store } from "../store/index.ts";',
      }),
    );
    expect(violations).toEqual([]);
  });

  it('rejects the filesystem outside store, config and api/static', () => {
    for (const specifier of ['node:fs', 'node:fs/promises', 'fs']) {
      const violations = checkBoundaries(
        files({ 'core/src/ingest/index.ts': `import { readFileSync } from "${specifier}";` }),
      );
      expect(violations[0]?.rule).toContain('only store, config and api/static');
    }
  });

  it('allows the filesystem in store, config and api/static', () => {
    const violations = checkBoundaries(
      files({
        'core/src/store/chunks.ts': 'import { openSync } from "node:fs";',
        'core/src/config/index.ts': 'import { readFileSync } from "node:fs";',
        'core/src/api/static.ts': 'import { statSync } from "node:fs";',
        'core/test/store.test.ts': 'import { mkdtempSync } from "node:fs";',
        'scripts/boundaries.ts': 'import { readFileSync } from "node:fs";',
      }),
    );
    expect(violations).toEqual([]);
  });

  it('rejects a module calling against the direction of the components table', () => {
    const violations = checkBoundaries(
      files({
        'core/src/store/index.ts': 'import { Control } from "../control/index.ts";',
        'core/src/control/index.ts': 'import { attachIngest } from "../ingest/index.ts";',
        'core/src/api/index.ts': 'import { attachIngest } from "../ingest/index.ts";',
      }),
    );
    expect(violations.map((v) => v.rule)).toEqual([
      'store may not call control (docs/architecture.md#components)',
      'control may not call ingest (docs/architecture.md#components)',
      'api may not call ingest (docs/architecture.md#components)',
    ]);
  });

  it('allows the calls the components table lists', () => {
    const violations = checkBoundaries(
      files({
        'core/src/api/index.ts':
          'import type { Store } from "../store/index.ts"; import type { Control } from "../control/index.ts";',
        'core/src/ingest/index.ts': 'import type { AgentControl } from "../control/index.ts";',
        'core/src/control/index.ts': 'import type { Store } from "../store/index.ts";',
      }),
    );
    expect(violations).toEqual([]);
  });
  it('lets only the daemon and the same vendor import a vendor package of the agent', () => {
    const claude =
      'import (\n\t"context"\n\n\tclaudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"\n)\n';
    const violations = checkBoundaries(
      files({
        'agent/internal/daemon/daemon.go': claude,
        'agent/internal/runtime/claude/driver.go': claude,
        'agent/internal/sync/reconcile_test.go': claude,
        'agent/internal/projects/file.go': claude,
        'agent/internal/workspace/install.go':
          'import "github.com/yangflow/hoplet/agent/internal/runtime/claude"\n',
        'agent/internal/runtime/manager.go':
          'import (\n\t"github.com/yangflow/hoplet/agent/internal/runtime/runtimetest"\n)\n',
      }),
    );
    expect(violations.map((v) => v.file)).toEqual([
      'agent/internal/projects/file.go',
      'agent/internal/workspace/install.go',
    ]);
    expect(violations[0]?.rule).toContain('ADR 0014');
  });
});
