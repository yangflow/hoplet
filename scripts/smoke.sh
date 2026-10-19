#!/usr/bin/env bash
# 端到端冒烟测试：大会话初始同步 + 增量同步 + 游标精确性 + 分页读取
set -euo pipefail
cd "$(dirname "$0")/.."

TMP=$(mktemp -d /tmp/hoplet-smoke.XXXXXX)
PORT=7799
TOKEN=dev-token
SESSION_ID="11111111-2222-4333-8444-555555555555"
PROJECT="$TMP/project"
CODEX_ID="019a0000-aaaa-7bbb-8ccc-dddddddddddd"
CODEX_DIR="$TMP/codex/sessions/2026/01/01"
FIXTURE_DIR="$TMP/claude-projects/${PROJECT//\//-}"
AGENT="$TMP/hoplet-agent"
LOG_SERVER="$TMP/server.log"
LOG_AGENT="$TMP/agent.log"
LOG_NODE="$TMP/node-err.log"

cleanup() {
  for pid in "${AGENT_PID:-}" "${SERVER_PID:-}"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  for pid in "${AGENT_PID:-}" "${SERVER_PID:-}"; do
    [[ -n "$pid" ]] && wait "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
fail() {
  echo "FAIL: $*"
  echo "--- server.log ---"; tail -5 "$LOG_SERVER" 2>/dev/null
  echo "--- agent.log ---"; tail -5 "$LOG_AGENT" 2>/dev/null
  echo "--- node-err.log ---"; cat "$LOG_NODE" 2>/dev/null
  echo "artifacts: $TMP"
  exit 1
}

# 从 /api/sessions 响应提取 "lineCount byteSize" 或 "none"（会话 id 走 argv，避免引号地狱）
extract_state() {
  echo "$1" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{try{const sid=process.argv[1];const s=JSON.parse(d).sessions.find(x=>x.sessionId===sid);console.log(s?`${s.lineCount} ${s.byteSize}`:"none")}catch{console.log("none")}})' "$2" 2>"$LOG_NODE"
}

api() { curl -sf -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT$1"; }

echo "Building current-source agent…"
(cd agent && go build -o "$AGENT" ./cmd/hoplet-agent)

echo "① 生成仿真大会话（12000 回合 ≈ 23MB）…"
mkdir -p "$FIXTURE_DIR" "$PROJECT"
FIXTURE="$FIXTURE_DIR/$SESSION_ID.jsonl"
node scripts/gen-fixture.mjs "$FIXTURE" 12000 "$PROJECT" > /dev/null
RAW_SIZE=$(wc -c < "$FIXTURE" | tr -d ' ')
LINES=$(wc -l < "$FIXTURE" | tr -d ' ')
echo "   会话 ${SESSION_ID}: $LINES 行, $((RAW_SIZE / 1024 / 1024))MB"

mkdir -p "$CODEX_DIR"
CODEX_FILE="$CODEX_DIR/rollout-2026-01-01T00-00-00-$CODEX_ID.jsonl"
node scripts/gen-codex-fixture.mjs "$CODEX_FILE" "$CODEX_ID" "$PROJECT" 50
CODEX_SIZE=$(wc -c < "$CODEX_FILE" | tr -d ' ')
CODEX_LINES=$(wc -l < "$CODEX_FILE" | tr -d ' ')

echo "② 启动 server 与 agent…"
HOPLET_TOKEN=$TOKEN HOPLET_DATA="$TMP/server-data" HOPLET_PORT=$PORT node core/src/main.ts > "$LOG_SERVER" 2>&1 &
SERVER_PID=$!
# Probe only the core started by this run, never an existing listener on the fixed port.
for i in $(seq 1 20); do
  kill -0 "$SERVER_PID" 2>/dev/null || fail "smoke server exited before becoming ready"
  if grep -q '"message":"listening"' "$LOG_SERVER" && api /api/health >/dev/null 2>&1; then break; fi
  [[ $i -eq 20 ]] && fail "smoke server not ready"
  sleep 0.5
done

AGENT_TOKEN=$(curl -sf -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"smoke-test","deviceId":"smoke-agent"}' \
  "http://127.0.0.1:$PORT/api/devices" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{console.log(JSON.parse(d).token)})')
[[ -n "$AGENT_TOKEN" ]] || fail "mint device token failed"

mkdir -p "$TMP/agent-home"
cat > "$TMP/agent-home/config.json" <<EOF
{ "server_url": "http://127.0.0.1:$PORT", "token": "$AGENT_TOKEN",
  "device_id": "smoke-agent",
  "claude_projects": "$TMP/claude-projects", "codex_home": "$TMP/codex", "device_name": "smoke-test",
  "poll_interval_ms": 400, "hoplet_home": "$TMP/agent-home",
  "exclude_prefixes": [] }
EOF
cat > "$TMP/agent-home/projects.json" <<EOF
{ "projects": [
  { "path": "$PROJECT",
    "imported": ["$SESSION_ID", "$CODEX_ID"] }
] }
EOF
"$AGENT" --config "$TMP/agent-home/config.json" daemon > "$LOG_AGENT" 2>&1 &
AGENT_PID=$!

echo "③ 等待初始全量同步（$((RAW_SIZE / 1024 / 1024))MB）…"
T0=$(date +%s)
for i in $(seq 1 120); do
  GOT=$(extract_state "$(api /api/sessions || true)" "$SESSION_ID")
  [[ "$GOT" == "$LINES $RAW_SIZE" ]] && break
  [[ $i -eq 120 ]] && fail "初始同步超时，最后状态: $GOT (期望 $LINES $RAW_SIZE)"
  sleep 1
done
T1=$(date +%s)
echo "   初始同步完成：${LINES} 行 / ${RAW_SIZE} 字节，用时 $((T1 - T0))s"

echo "④ 增量追加 200 回合（≈1.8MB），只应传新增量…"
node -e '
const fs = require("fs");
const fd = fs.openSync(process.argv[1], "a");
const sid = "11111111-2222-4333-8444-555555555555";
let ts = Date.now();
const iso = () => new Date((ts += 30000)).toISOString();
for (let i = 0; i < 200; i++) {
  const mk = (type, message, extra = {}) => fs.writeSync(fd, JSON.stringify({ type, sessionId: sid, timestamp: iso(), cwd: process.argv[2], ...extra, message }) + "\n");
  mk("user", { role: "user", content: "增量第 " + i + " 轮：大文件还是卡吗？" });
  mk("assistant", { role: "assistant", content: [{ type: "text", text: "增量只传新增字节，" + i + " 轮验证通过。" }, { type: "tool_use", id: "t" + i, name: "Read", input: { file_path: "/x/y.ts" } }] });
  mk("user", { role: "user", content: [{ type: "tool_result", tool_use_id: "t" + i, content: "ok " + i + " " + "B".repeat(8000) }] }, { isMeta: false, isSidechain: false });
}
fs.closeSync(fd);
' "$FIXTURE" "$PROJECT"
NEW_LINES=$((LINES + 600))
T2=$(date +%s)
for i in $(seq 1 30); do
  GOT=$(extract_state "$(api /api/sessions || true)" "$SESSION_ID")
  LC=${GOT%% *}; BS=${GOT##* }
  if [[ "$LC" == "$NEW_LINES" ]]; then
    FILE_NOW=$(wc -c < "$FIXTURE" | tr -d ' ')
    if [[ "$BS" != "$FILE_NOW" ]]; then fail "字节游标不精确: server=$BS file=$FILE_NOW"; fi
    break
  fi
  [[ $i -eq 30 ]] && fail "增量同步超时: $GOT (期望行数 $NEW_LINES)"
  sleep 1
done
T3=$(date +%s)
echo "   增量同步完成：+600 行，用时 $((T3 - T2))s，游标与服务端字节完全一致 ✓"

echo "⑤ 分页读取验证…"
TAIL=$(api "/api/sessions/$SESSION_ID/lines?start=-50&limit=50")
echo "$TAIL" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{const p=JSON.parse(d);const ok=p.end-p.start===50&&p.start===p.total-50;const tail3=p.entries.slice(-3).map(x=>JSON.stringify(x)).join("");const last=tail3.includes("增量第 199 轮")&&tail3.includes("ok 199");console.log(ok&&last?"tail-ok":"tail-bad")})' 2>"$LOG_NODE" | grep -q tail-ok || fail "尾部分页异常"
HEAD=$(api "/api/sessions/$SESSION_ID/lines?start=0&limit=3")
echo "$HEAD" | grep -q "帮我优化 hoplet 会话同步" || fail "头部行内容不符"
TITLE=$(api /api/sessions | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{const s=JSON.parse(d).sessions.find(x=>x.sessionId==="11111111-2222-4333-8444-555555555555");console.log(s.title.includes("帮我优化")?"title-ok":"title-bad:"+s.title)})' 2>"$LOG_NODE")
[[ "$TITLE" == "title-ok" ]] || fail "标题提取失败"
echo "   尾部分页(50行) ✓ 头部行 ✓ 标题提取 ✓"

echo "⑥ 搜索验证（跨 gzip 块 + 大会话尾部 + Hoplet 全局搜索）…"
SEARCH=$(api "/api/sessions/$SESSION_ID/search?q=%E5%A2%9E%E9%87%8F%E7%AC%AC%20199%20%E8%BD%AE")
echo "$SEARCH" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{const r=JSON.parse(d);const ok=r.total===1&&r.hits[0]&&r.hits[0].lineNo>34000;console.log(ok?"search-ok":"search-bad:"+JSON.stringify(r).slice(0,120))})' 2>"$LOG_NODE" | grep -q search-ok || fail "大会话搜索异常"
GSEARCH=$(api "/api/search?q=%E5%A2%9E%E9%87%8F%E7%AC%AC%20199%20%E8%BD%AE")
echo "$GSEARCH" | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{const r=JSON.parse(d);const hit=(r.results||[]).find(x=>x.sessionId==="11111111-2222-4333-8444-555555555555");const ok=hit&&hit.lineNo>34000;console.log(ok?"gsearch-ok":"gsearch-bad:"+JSON.stringify(r).slice(0,120))})' 2>"$LOG_NODE" | grep -q gsearch-ok || fail "全局搜索异常"
echo "   大会话跨块搜索 ✓ 全局搜索 ✓"

echo "⑦ Codex 会话（rollout，线程 ID 即会话 ID）…"
for i in $(seq 1 30); do
  GOT=$(extract_state "$(api /api/sessions || true)" "$CODEX_ID")
  [[ "$GOT" == "$CODEX_LINES $CODEX_SIZE" ]] && break
  [[ $i -eq 30 ]] && fail "Codex 会话同步超时: $GOT (期望 $CODEX_LINES $CODEX_SIZE)"
  sleep 1
done
echo "   Codex 会话 ${CODEX_LINES} 行 / ${CODEX_SIZE} 字节，游标与服务端一致 ✓"
api /api/sessions | node -e '
let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{
  const s=JSON.parse(d).sessions;
  const ok=s.length===2&&s.some(x=>x.sessionId===process.argv[2]&&x.projectPath===process.argv[1])&&s.some(x=>x.sessionId===process.argv[3]&&x.provider==="codex"&&x.projectPath===process.argv[1]);
  process.exit(ok?0:1);
})' "$PROJECT" "$SESSION_ID" "$CODEX_ID" 2>"$LOG_NODE" || fail "unexpected sessions outside synthetic fixtures"


STORED=$(du -sk "$TMP/server-data/sessions" | cut -f1)
echo ""
echo "PASS ✓  原始 $(($RAW_SIZE / 1024))KB → 服务端落盘 ${STORED}KB（gzip 分块）；产物保留在 $TMP"
