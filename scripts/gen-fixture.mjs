// Generates a realistic Claude Code session JSONL for the smoke test.
// Usage: node gen-fixture.mjs <output file> [turns=2000] [cwd]
import { closeSync, openSync, writeSync } from 'node:fs';

const out = process.argv[2];
const turns = Number(process.argv[3] ?? 2000);
const sessionId =
  out
    .split('/')
    .pop()
    ?.replace(/\.jsonl$/, '') ?? 'unknown';
const cwd = process.argv[4] ?? '/Users/mark/github/yangflow/hoplet-fixture';

let ts = Date.now() - turns * 30_000;
const iso = () => {
  ts += 30_000;
  return new Date(ts).toISOString();
};
const fd = openSync(out, 'w');
let bytes = 0;
function line(obj) {
  const s = `${JSON.stringify(obj)}\n`;
  bytes += Buffer.byteLength(s);
  writeSync(fd, s);
}

line({ type: 'queue-operation', operation: 'user', sessionId, timestamp: iso() });

for (let i = 0; i < turns; i++) {
  line({
    type: 'user',
    sessionId,
    timestamp: iso(),
    cwd,
    isSidechain: false,
    isMeta: false,
    userType: 'external',
    message: {
      role: 'user',
      content:
        i === 0
          ? '帮我优化 hoplet 会话同步的增量算法，86MB 的大会话打开还是有点慢'
          : `继续，第 ${i} 轮：检查一下 chunk 滚动逻辑和游标对齐`,
    },
  });
  line({
    type: 'assistant',
    sessionId,
    timestamp: iso(),
    cwd,
    message: {
      role: 'assistant',
      content: [
        {
          type: 'thinking',
          thinking: `第 ${i} 轮：先看 tailer 的偏移推进，再验证 gzip 成员追加。`.repeat(3),
        },
        {
          type: 'text',
          text: `好的。第 ${i} 轮我改了 append 路径：块满 500 行滚动并压缩，游标按原始字节推进，重同步直接 seek 到 byteSize。`,
        },
        {
          type: 'tool_use',
          id: `toolu_${i}`,
          name: 'Bash',
          input: { command: `go test ./internal/sync -run TestCursor -v # round ${i}` },
        },
      ],
    },
  });
  const big = i % 100 === 0 ? 'A'.repeat(60_000) : '';
  line({
    type: 'user',
    sessionId,
    timestamp: iso(),
    cwd,
    isSidechain: false,
    isMeta: false,
    message: {
      role: 'user',
      content: [
        {
          type: 'tool_result',
          tool_use_id: `toolu_${i}`,
          content: `PASS\n游标对齐验证 OK${big ? `\nbase64:${big}` : ''}`,
        },
      ],
    },
  });
}
closeSync(fd);
console.log(JSON.stringify({ sessionId, bytes, lines: turns * 3 + 1 }));
