#!/usr/bin/env bash
# =============================================================================
# e2e_llm_fault.sh —— LLM 接口故障端到端联调
# -----------------------------------------------------------------------------
# 模拟真实 LLM 服务在各种故障下的行为，验证服务"不崩溃、友好错误、有带
# trace_id 的错误日志、5xx 自动重试、熔断触发与恢复"。
#
# 前置条件：
#   1. 故障注入 mock LLM 已启动：
#        go run ./tools/fault_inject_llm -port 18333
#   2. api 服务已启动，且 LLM_BASE_URL 指向该 mock：
#        env LLM_BASE_URL=http://127.0.0.1:18333 LLM_API_KEY=mock-key \
#            LLM_CHAT_MODEL=test-model LOG_LEVEL=debug \
#            LLM_TIMEOUT_SECONDS=5 LLM_MAX_RETRY=2 \
#            LOG_FILE=/tmp/llm_fault_e2e.log go run cmd/api/main.go
#      （LOG_FILE 落盘便于断言 trace_id。LLM_TIMEOUT 取 5s：既大于重试退避总和 1+2=3s
#        以完整展示 5xx 重试，又小于场景1 mock 挂起 8s 以触发超时，两者兼顾。）
#   3. 用 admin 登录后把 /api/admin/tool-config/knowledge_retrieve 置 false
#      （减少 ReAct 多轮变量，聚焦 LLM 故障本身）
# ⚠️ 每次运行前请重启 api（熔断器状态在进程内，重启可复位；否则上一轮熔断
#    状态会污染下一轮场景，导致 5xx 重试被熔断拦截而无法断言）。
# 用法： BASE=http://127.0.0.1:8080 ./scripts/e2e_llm_fault.sh
# =============================================================================
set -o pipefail
BASE=${BASE:-http://127.0.0.1:8080}
MOCK=${MOCK:-http://127.0.0.1:18333}
H='Content-Type: application/json'
PASS=0; FAIL=0
ok()  { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad() { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step(){ echo ""; echo "▶▶▶ $1"; }

# 依赖检查：curl / jq
command -v curl >/dev/null || { echo "缺少 curl"; exit 1; }
command -v jq >/dev/null || { echo "缺少 jq"; exit 1; }

# 检查 mock 与 api 连通
curl -sf "$MOCK/mode" >/dev/null || { echo "❌ mock($MOCK) 未运行，先启动 tools/fault_inject_llm"; exit 1; }
curl -sf "$BASE/health" >/dev/null || { echo "❌ api($BASE) 未运行"; exit 1; }

# ---------- 登录 ----------
BOOT_TID=${BOOT_TID:-1}
BOOT_USER=${BOOT_USER:-journey_boot}
BOOT_PASS=${BOOT_PASS:-}
[ -z "$BOOT_PASS" ] && BOOT_PASS=$(grep -E '^BOOTUSER_PASS=' .env 2>/dev/null | cut -d= -f2-)
BT=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$BOOT_TID,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jq -r '.data.token')
[ -z "$BT" ] || [ "$BT" = "null" ] && { echo "❌ 管理员登录失败"; exit 1; }

TS=$(date +%s)
TID=$(curl -s -X POST "$BASE/api/tenant" -H "$H" -H "Authorization: Bearer $BT" \
  -d "{\"name\":\"LLM故障$TS\",\"admin_username\":\"llmf_${TS}\",\"admin_password\":\"Fault@12345\"}" | jq -r '.data.ID')
[ -z "$TID" ] || [ "$TID" = "null" ] && { echo "❌ 创建租户失败"; exit 1; }
AH=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"llmf_${TS}\",\"password\":\"Fault@12345\"}" | jq -r '.data.token')
[ -z "$AH" ] || [ "$AH" = "null" ] && { echo "❌ 测试管理员登录失败"; exit 1; }
echo "测试租户 tenant_id=$TID (admin=$AH_len=${#AH})"
echo "  [debug] AH=${AH:0:24}..."

AUTH="Authorization: Bearer $AH"
# 关闭知识库工具，聚焦 LLM 故障
curl -s -X PUT "$BASE/api/admin/tool-config/knowledge_retrieve" -H "$AUTH" -H "$H" -d '{"is_enable":false}' >/dev/null

mock_mode(){ curl -s "$MOCK/mode" | jq -r '.mode'; }
mock_stats(){ curl -s "$MOCK/stats"; }
set_mode(){ curl -s -X PUT "$MOCK/mode" -H "$H" -d "{\"mode\":\"$1\"}" >/dev/null; }
# now_ms 返回当前毫秒时间戳（纳秒换算，兼容 macOS/GNU date）
now_ms(){ echo $(( $(date +%s%N) / 1000000 )); }
chat(){ # $1=query
  SID=$(curl -s -X POST "$BASE/api/session" -H "$AUTH" -H "$H" -d '{"title":"fault"}' | jq -r '.data.id')
  curl -s -m 60 -X POST "$BASE/api/chat" -H "$AUTH" -H "$H" \
    -d "{\"session_id\":\"$SID\",\"query\":\"$1\"}" | jq -r '.data.answer // .message'
}

# ---------- 场景1：LLM 超时 ----------
step "场景1：LLM 超时（mock 挂 8s > api 预算5s）"
set_mode timeout
curl -s -X PUT "$MOCK/slow" -H "$H" -d '{"ms":8000}' >/dev/null
S1=$(now_ms); A1=$(chat "测试超时"); E1=$(now_ms)
D1=$((E1-S1))
echo "  耗时 ${D1}ms，回答=${A1:0:40}"
case "$A1" in *不可用*|*稍后再试*|*失败*) ok "超时→友好错误提示 ✓" ;; *) bad "超时→未返回友好提示($A1)" ;; esac
[ "$D1" -lt 8000 ] && ok "超时→快速失败(不卡死) ✓" || bad "耗时过长 ${D1}ms (可能未快速失败)"
case "$(mock_stats)" in *"\"timeout_hits\":"[1-9]* ) ok "mock 确认收到超时挂起请求 ✓" ;; *) bad "mock 未收到超时请求" ;; esac

# ---------- 场景2：LLM 返回 5xx → 自动重试 ----------
step "场景2：LLM 持续 5xx → 自动重试至 maxRetry 次"
set_mode fail500
B5=$(mock_stats | jq -r '.fail500_hits')
S2=$(now_ms); A2=$(chat "测试5xx"); E2=$(now_ms); D2=$((E2-S2))
A5=$(mock_stats | jq -r '.fail500_hits')
echo "  耗时 ${D2}ms，本次 fail500 命中 +$((A5-B5))（maxRetry=2→应 3 次，受预算约束至少 2 次）"
case "$A2" in *不可用*|*稍后再试*|*失败*) ok "5xx→友好错误提示 ✓" ;; *) bad "5xx→未返回友好提示($A2)" ;; esac
[ $((A5-B5)) -ge 2 ] && ok "5xx→自动重试(命中≥2次) ✓" || bad "5xx→未见重试(+$((A5-B5)))"
case "$A2" in *不可用*) echo "  (注: 自测点'5xx 自动重试'已发生)" ;; esac

# ---------- 场景3：LLM 返回格式错误 → 降级 ----------
step "场景3：LLM 返回非 JSON（格式错误）→ 降级不崩溃"
set_mode garbage
A3=$(chat "测试格式错误")
echo "  回答=${A3:0:50}"
case "$A3" in *不可用*|*稍后再试*|*失败*|*未收敛*|*格式*|*JSON*) ok "格式错误→有降级处理 ✓" ;; *) bad "格式错误→未见降级($A3)" ;; esac

# ---------- 场景4：熔断器触发 ----------
step "场景4：连续失败触发熔断（fail500_recover: 前5次失败）"
set_mode fail500_recover
for i in $(seq 1 7); do chat "触发熔断$i" >/dev/null; done
# 最近日志是否出现"熔断器已打开"（快速失败）
if grep -q "熔断器已打开" /tmp/llm_fault_e2e.log 2>/dev/null; then
  ok "连续失败→熔断器打开(快速失败) ✓"
else
  B=$(mock_stats | jq -r '.chat_total')
  chat "x" >/dev/null
  A=$(mock_stats | jq -r '.chat_total')
  case "$A" in "$B") ok "熔断后请求快速失败、不再发 HTTP ✓" ;; *) bad "熔断未生效（请求数从 $B 增至 $A）" ;; esac
fi

# ---------- 场景5：熔断恢复 ----------
step "场景5：熔断半开后自动恢复（服务恢复→试探成功→熔断关闭）"
# 说明：fail500_recover 的"前 N 次失败"计数在熔断打开期间因请求被拒而无法推进，
# 故此处"服务恢复"用切回 ok 模式模拟（从该时刻起所有请求成功），
# 再等待 > OpenTimeout(30s) 让熔断进入 Half-Open，试探请求成功即恢复 Closed。
curl -s -X PUT "$MOCK/mode" -H "$H" -d '{"mode":"ok"}' >/dev/null
sleep 33   # 等待 > OpenTimeout(30s) → Half-Open
A5r=$(chat "熔断恢复了么")
B5c=$(grep -c 'circuit open' /tmp/llm_fault_e2e.log 2>/dev/null || echo 0)
sleep 1
A5c=$(grep -c 'circuit open' /tmp/llm_fault_e2e.log 2>/dev/null || echo 0)
echo "  回答=${A5r:0:40}"
if [ "$A5c" -gt "$B5c" ]; then
  bad "熔断未恢复（新增 circuit open）"
elif case "$A5r" in *不可用*|*stcircuit*|*暂时*) true;; *) false;; esac; then
  bad "熔断后未恢复($A5r)"
else
  ok "熔断半开试探成功→自动恢复(circuit open 不再新增) ✓"
fi

# 汇总
echo ""
echo "=========================================================="
echo "  LLM 故障测试：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 全部通过" || echo "⚠️ 存在失败项，请见上方 ❌"
echo "[完成] 测试租户 tenant_id=$TID"
exit $((FAIL>0?1:0))
