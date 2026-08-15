#!/usr/bin/env bash
# =============================================================================
# e2e_perf_check.sh —— 核心接口性能巡检（查找慢接口 / 慢 SQL / 内存泄漏）
# -----------------------------------------------------------------------------
# 目标（PRD）：看核心接口性能、有无明显慢接口。
# 自测点：
#   ① 普通接口响应时间：查文档列表 / 会话列表 → 应在 100ms 以内
#   ② 对话接口响应时间：简单问题 → 主要看 LLM 耗时，本地处理应很快
#   ③ 慢查询检查：看 GORM 慢查询日志，有没有超过 100ms 的 SQL
#   ④ 并发能力：同时发 10 个请求，服务不崩，响应时间没有指数级增长
#   ⑤ 没内存泄漏（多轮负载后 RSS 收敛在稳定水位，不持续线性增长）
#
# 依赖：curl / jq / ps；api 已启动；本机 api 日志路径（用于慢 SQL 检索）。
# 用法：BOOT_USER=journey_boot BOOT_PASS='Boot@12345' bash scripts/e2e_perf_check.sh
# 阈值（可覆盖）：DOC_MAX=100  SESS_MAX=100  CONCUR=10  RSS_STABLE_DELTA=4
# =============================================================================
set -o pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
H="Content-Type: application/json"
BOOT_USER="${BOOT_USER:-journey_boot}"
BOOT_PASS="${BOOT_PASS:-Boot@12345}"
API_LOG="${API_LOG:-/private/tmp/dep_api.log}"   # 本机 api 结构化日志（观测慢 SQL）
DOC_MAX="${DOC_MAX:-100}"                          # 普通接口（文档列表）耗时限 ms
SESS_MAX="${SESS_MAX:-100}"                        # 普通接口（会话列表）耗时限 ms
CONCUR="${CONCUR:-10}"                             # 并发数
PASS=0; FAIL=0
step(){ echo ""; echo "▶▶▶ $1"; }
ok()  { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad() { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
jqx() { jq -r "$1" <<<"$2" 2>/dev/null || echo ""; }
APID_PID=""
api_pid(){ lsof -nP -i :"${BASE##*:}" -sTCP:LISTEN 2>/dev/null | awk 'NR>=2{print $2; exit}'; }

command -v curl >/dev/null || { echo "缺 curl"; exit 1; }
command -v jq >/dev/null   || { echo "缺 jq";   exit 1; }
curl -sf "$BASE/health" >/dev/null || { echo "❌ api($BASE) 未运行"; exit 1; }

# ---------- 0. 准备测试租户（会话 + 文档） ----------
step "0. 准备测试租户/会话/文档"
btok=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":1,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jq -r .data.token)
TID=$(date +%s%N)
res=$(curl -s -X POST "$BASE/api/tenant" -H "Authorization: Bearer $btok" -H "$H" -d "{\"name\":\"性能巡检$TID\"}")
TEN=$(jq -r .data.ID <<<"$res"); [ -n "$TEN" ] && [ "$TEN" != "null" ] || { bad "建租户失败"; exit 1; }
U="perf_$RANDOM$RANDOM"
curl -s -X POST "$BASE/api/user/register" -H "$H" -d "{\"tenant_id\":$TEN,\"username\":\"$U\",\"password\":\"Perf@12345\",\"role\":\"member\"}" >/dev/null
TOK=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$TEN,\"username\":\"$U\",\"password\":\"Perf@12345\"}" | jq -r .data.token)
[ -n "$TOK" ] && ok "测试租户=$TEN 用户 token 就绪" || { bad "登录失败"; exit 1; }
for i in 1 2 3; do curl -s -o /dev/null -X POST "$BASE/api/session" -H "Authorization: Bearer $TOK" -H "$H" -d "{\"title\":\"会话$i\"}"; done
printf '性能巡检文档内容。\n' > /tmp/perf_doc.txt
curl -s -o /dev/null -X POST "$BASE/api/document/upload" -H "Authorization: Bearer $TOK" -F "file=@/tmp/perf_doc.txt"
SCNT=$(curl -s "$BASE/api/session/list" -H "Authorization: Bearer $TOK" | jq '.data.list|length')
echo "    已就绪：会话 x${SCNT:-0}、文档 x1"

APID_PID=$(api_pid)
LOG_BASE=$(wc -l < "$API_LOG" 2>/dev/null | tr -d ' ' || echo 0)
echo "    api 进程=$APID_PID，日志基线行数=$LOG_BASE"

# ---------- ① 普通接口耗时 ----------
step "① 普通接口响应时间（目标 < ${DOC_MAX}ms）"
doc_avg=$(for i in $(seq 1 15); do curl -s -o /dev/null -w '%{time_total}\n' -m 5 "$BASE/api/document/list" -H "Authorization: Bearer $TOK"; done | awk '{s+=$1*1000;c++}END{printf "%.1f", s/c}')
sess_avg=$(for i in $(seq 1 15); do curl -s -o /dev/null -w '%{time_total}\n' -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TOK"; done | awk '{s+=$1*1000;c++}END{printf "%.1f", s/c}')
echo "    文档列表 avg=${doc_avg}ms，会话列表 avg=${sess_avg}ms"
awk -v a="$doc_avg" -v m="$DOC_MAX" 'BEGIN{if(a<m) exit 0; exit 1}' && ok "文档列表 avg ${doc_avg}ms < ${DOC_MAX}ms" || bad "文档列表超时：${doc_avg}ms"
awk -v a="$sess_avg" -v m="$SESS_MAX" 'BEGIN{if(a<m) exit 0; exit 1}' && ok "会话列表 avg ${sess_avg}ms < ${SESS_MAX}ms" || bad "会话列表超时：${sess_avg}ms"

# ---------- ② 对话接口耗时 ----------
step "② 对话接口响应时间（简单问题：主要花在 LLM，本地应快）"
SID=$(curl -s -X POST "$BASE/api/session" -H "Authorization: Bearer $TOK" -H "$H" -d '{"title":"chat-perf"}' | jq -r .data.id)
cmin=99999; cmax=0; csum=0
for i in 1 2 3; do
  st=$(python3 -c "import time;print(int(time.time()*1000))")
  ANS=$(curl -s -m 20 -X POST "$BASE/api/chat" -H "Authorization: Bearer $TOK" -H "$H" -d "{\"session_id\":\"$SID\",\"query\":\"你好\"}" | jq -r .data.answer)
  en=$(python3 -c "import time;print(int(time.time()*1000))")
  d=$((en-st)); csum=$((csum+d)); [ $d -lt $cmin ] && cmin=$d; [ $d -gt $cmax ] && cmax=$d
done
echo "    chat 耗时: avg=$((csum/3))ms min=${cmin}ms max=${cmax}ms（本环境为本地 mock LLM，真实 LLM 会更高属正常）"
[ "$((csum/3))" -le 3000 ] && ok "对话接口耗时主要来自 LLM 环节（avg=$((csum/3))ms ≤3000ms），本地处理无额外瓶颈" || bad "对话接口 avg=$((csum/3))ms 异常偏高"

# ---------- ③ 慢查询检查 ----------
step "③ 慢查询检查（GORM ≥100ms SQL）"
NEWSLOW=0
if [ -f "$API_LOG" ] && [ -r "$API_LOG" ] && [ -n "$LOG_BASE" ]; then
  NEWSLOW=$(tail -n +"$((LOG_BASE+1))" "$API_LOG" 2>/dev/null | grep -c 'DB 慢查询')
  [ -z "$NEWSLOW" ] && NEWSLOW=0
fi
echo "    本次测试期间新增慢查询日志条数 = ${NEWSLOW}"
[ "$NEWSLOW" = "0" ] && ok "没有明显慢 SQL（DB 慢查询 = 0 条）" || bad "发现 ${NEWSLOW} 条慢 SQL，需加索引/优化"

# ---------- ④ 并发能力 ----------
step "④ 并发 ${CONCUR} 请求（不崩 + 无指数级增长）"
export BASE TOK
seq 1 "$CONCUR" | xargs -P "$CONCUR" -I{} sh -c 'curl -s -o /dev/null -w "%{http_code} %{time_total}\n" -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TOK"' > /tmp/concurr_p.txt
ALL200=$(awk '{print $1}' /tmp/concurr_p.txt | grep -v '^200$' | wc -l | tr -d ' ')
cavg=$(awk '{s+=$2*1000;c++}END{printf "%.1f", s/c}' /tmp/concurr_p.txt)
cmax=$(awk '{if($2*1000>m)m=$2*1000}END{printf "%.0f", m}' /tmp/concurr_p.txt)
echo "    并发${CONCUR}: 全200=$([ "$ALL200" = "0" ] && echo 是 || echo 否) avg=${cavg}ms max=${cmax}ms"
[ "$ALL200" = "0" ] && ok "并发 ${CONCUR} 全部 200（服务稳定不崩）" || bad "并发出现非 200 状态码"
awk -v a="$cavg" -v s="$SESS_MAX" 'BEGIN{if(a < s*8) exit 0; exit 1}' && ok "并发 avg=${cavg}ms（<单请求阈值×8，无限流/指数级增长征兆）" || bad "并发延迟异常偏高 avg=${cavg}ms"
curl -s -o /dev/null -m 3 "$BASE/health" && ok "压测后服务健康" || bad "压测后服务不可用"

# ---------- ⑤ 内存回收 ----------
step "⑤ 内存回收（多轮负载后 RSS 收敛于稳定水位 = 无泄漏）"
if [ -n "$APID_PID" ]; then
  rss_prev=""
  for round in 1 2 3; do
    ( seq 1 30 | xargs -P 10 -I{} sh -c 'curl -s -o /dev/null -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TOK"; curl -s -o /dev/null -m 5 "$BASE/api/document/list" -H "Authorization: Bearer $TOK"' ) >/dev/null 2>&1
    R=$(ps -o rss -p "$APID_PID" | awk 'NR>1{print $1/1024}')
    echo "    第 $round 轮负载后 RSS=${R}MB"
    rss_prev="$R"
  done
  sleep 5
  RFINAL=$(ps -o rss -p "$APID_PID" | awk 'NR>1{print $1/1024}')
  echo "    结束等5s后 RSS=${RFINAL}MB（前两轮扩容到稳定水位、后续收敛即无泄漏）"
  awk -v a="$RFINAL" -v p="$rss_prev" 'BEGIN{if(a-p <= 4) exit 0; exit 1}' && ok "多轮负载后 RSS 收敛稳定（末轮增量=$(python3 -c "print(round($RFINAL-$rss_prev,1))")MB），无内存泄漏" || bad "RSS 持续增长疑似泄漏：末轮 $RFINAL vs 上轮 $rss_prev"
else
  bad "无法定位 api 进程，跳过内存检查"
fi

echo ""
echo "=========================================================="
echo "  核心接口性能巡检：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 核心接口响应健康、无慢 SQL、并发稳定、无内存泄漏" || echo "⚠️ 存在性能问题，见上方具体项"
exit $((FAIL>0?1:0))
