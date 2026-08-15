#!/usr/bin/env bash
# =============================================================================
# e2e_rate_limit.sh —— 限流功能端到端验证
# -----------------------------------------------------------------------------
# 场景（PRD）：
#   用脚本快速发请求（如 1 秒发 100 个）；超过阈值返回 429；等窗口过期自动恢复；
#   租户 A 限流不影响租户 B；对话接口限流比普通接口更严格。
#
# 自测点：
#   ① 超限返回 429（普通接口：用户级 UserPerMin=60，第 61 个起 429）
#   ② 限流后自动恢复（等待窗口 60s 后同用户再发恢复 200）
#   ③ 多租户限流独立（租户 A 打满 → 全新租户 C 正常 200，不受 A 影响）
#   ④ 对话接口限流更严格（chat 专属 ChatPerMin=20，第 21 个起 429，远比普通 60 早）
#
# 默认阈值来自 config（RATE_LIMIT_* 环境变量）：租户300 / 用户60 / 对话20 / 窗口60s。
# 依赖：curl / jq；api + worker 已启动；Redis 正常（限流走 Redis 滑动窗口）。
# 用法：BOOT_USER=journey_boot BOOT_PASS='Boot@12345' bash scripts/e2e_rate_limit.sh
# 说明：含 62s 等恢复，完整跑约 2 分钟。
# =============================================================================
set -o pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
H="Content-Type: application/json"
BOOT_USER="${BOOT_USER:-journey_boot}"
BOOT_PASS="${BOOT_PASS:-Boot@12345}"
WINDOW="${WINDOW:-62}"   # 等待恢复的秒数（应略大于限流窗口）

PASS=0; FAIL=0
step(){ echo ""; echo "▶▶▶ $1"; }
ok()  { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad() { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
jqx() { jq -r "$1" <<<"$2" 2>/dev/null || echo ""; }

command -v curl >/dev/null || { echo "缺少 curl"; exit 1; }
command -v jq >/dev/null   || { echo "缺少 jq";   exit 1; }
curl -sf "$BASE/health" >/dev/null || { echo "❌ api($BASE) 未运行"; exit 1; }

newTenantUser() {
  # $1=租户名前缀；返回 "TENANT|TOKEN"
  local prefix="$1"
  local btok
  btok=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":1,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jq -r .data.token)
  local tidname; tidname=$(date +%s%N)
  local res; res=$(curl -s -X POST "$BASE/api/tenant" -H "Authorization: Bearer $btok" -H "$H" -d "{\"name\":\"$prefix$tidname\"}")
  local tenant; tenant=$(jq -r .data.ID <<<"$res")
  local uname; uname="rl_${prefix}_${RANDOM}"
  curl -s -X POST "$BASE/api/user/register" -H "$H" -d "{\"tenant_id\":$tenant,\"username\":\"$uname\",\"password\":\"RateA@123\",\"role\":\"member\"}" >/dev/null
  local tok; tok=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$tenant,\"username\":\"$uname\",\"password\":\"RateA@123\"}" | jq -r .data.token)
  echo "$tenant|$tok"
}

# ---------- 0. 引导 + 建租户 A ----------
step "0. 引导登录 + 建租户A/用户"
IFS='|' read -r TEN_A TKA <<<"$(newTenantUser 'rlA')"
[ -n "$TKA" ] && ok "租户A=$TEN_A 及用户 token 就绪" || { bad "建租户A失败"; exit 1; }

# ---------- ① 自测点1：超限返回 429（普通接口） ----------
step "① 普通接口（session/list）快速打满，验证超限返回 429"
rm -f /tmp/rl_codes1.txt
for i in $(seq 1 70); do
  code=$(curl -s -o /dev/null -w '%{http_code}' -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TKA")
  echo "$code" >> /tmp/rl_codes1.txt
done
OKCNT=$(grep -c '^200$' /tmp/rl_codes1.txt)
N429=$(grep -c '^429$' /tmp/rl_codes1.txt)
FIRST429=$(grep -n '^429$' /tmp/rl_codes1.txt | head -1 | cut -d: -f1)
echo "    200×$OKCNT, 429×$N429, 首个429在第 $FIRST429 次"
[ "$N429" -gt 0 ] && ok "普通接口超限返回 429（共 $N429 个）" || bad "普通接口未出现 429"
[ "$FIRST429" -ge 55 ] && [ "$FIRST429" -le 65 ] && ok "首个 429 出现在第 $FIRST429 次（≈用户阈值 60）" || bad "首个 429 位置 $FIRST429 偏离预期（用户阈值60）"

# ---------- ② 自测点2：限流后自动恢复 ----------
step "② 等待 ${WINDOW}s 窗口滑出，验证同用户自动恢复"
sleep "$WINDOW"
RC1=$(curl -s -o /dev/null -w '%{http_code}' -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TKA")
echo "    恢复后状态码=$RC1"
[ "$RC1" = "200" ] && ok "限流窗口过期后自动恢复（同用户再次请求返回 200）" || bad "恢复失败：返回 $RC1"

# ---------- ③ 自测点3：多租户限流独立 ----------
step "③ 多租户限流独立（租户A打满，全新租户C不受影响）"
# 重新打满租户 A 用户
for i in $(seq 1 65); do curl -s -o /dev/null -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TKA"; done
ALAST=$(curl -s -o /dev/null -w '%{http_code}' -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TKA")
echo "    租户A 已打满，再次请求 → $ALAST"
IFS='|' read -r TEN_C TKC <<<"$(newTenantUser 'rlC')"
CLIST=""
for i in 1 2 3; do
  code=$(curl -s -o /dev/null -w '%{http_code}' -m 5 "$BASE/api/session/list" -H "Authorization: Bearer $TKC")
  CLIST="$CLIST $code"
done
echo "    租户C（全新）3 次请求 → $CLIST"
if [ "$ALAST" = "429" ] && [ "$CLIST" = " 200 200 200" ]; then
  ok "租户A 限流(429)时租户C 全部 200 —— 多租户限流相互独立"
else
  bad "多租户独立验证失败：A=$ALAST, C=$CLIST（期望 C 全为 200）"
fi

# ---------- ④ 自测点4：对话接口限流更严格 ----------
step "④ 对话接口（/chat）限流更严格（专属阈值更低、更早拦截）"
IFS='|' read -r TEN_D TKD <<<"$(newTenantUser 'rlD')"
FIRST429C=0
for i in $(seq 1 35); do
  code=$(curl -s -o /dev/null -w '%{http_code}' -m 15 -X POST "$BASE/api/chat" -H "Authorization: Bearer $TKD" -H "$H" -d '{"query":"测试限流"}')
  if [ "$code" = "429" ]; then FIRST429C=$i; break; fi
done
echo "    chat 首个 429 出现在第 $FIRST429C 次请求（对话阈值 ChatPerMin=20，普通 60）"
# 对话更严格：chat 首个 429 应远早于普通接口阈值 60（且<=阈值附近）
if [ "$FIRST429C" -ge 15 ] && [ "$FIRST429C" -le 25 ]; then
  ok "对话接口在 $FIRST429C 次即触发 429（≈对话专属阈值 20，比普通接口 60 更严格早拦截）"
else
  bad "对话触发 429 位置 $FIRST429C 偏离预期（应在 15~25，ChatPerMin=20）"
fi

echo ""
echo "=========================================================="
echo "  限流功能验证：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 限流生效、超限429、窗口后自动恢复、多租户独立、对话更严格 —— 全部符合预期" || echo "⚠️ 存在限流不符项，需排查"
exit $((FAIL>0?1:0))
