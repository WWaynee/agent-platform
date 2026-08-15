#!/usr/bin/env bash
# =============================================================================
# e2e_session_isolation.sh —— 同一用户多会话并发隔离测试
# -----------------------------------------------------------------------------
# 场景（PRD）：
#   同一个用户创建 3 个会话；每个会话里说不同的内容：
#     会话1 "我叫张三"
#     会话2 "我叫李四"
#     会话3 "我叫王五"
#   三个会话里分别问 "我叫什么"（并发）
#   验证：每个会话的回答对应自己的上下文，不串；并发下会话之间不串数据。
#
# 自测点：
#   1. 不同会话上下文不串（各问"我叫什么"返回各自会话记住的名字）
#   2. 并发情况下依然隔离（三会话同时提问，各自返回正确名字）
#   3. 会话列表能看到所有会话
#
# 依赖：curl / jq；api + worker 已启动；mock/真实 LLM 就绪（mock 需支持
#       会话内自我介绍记忆，见 tools/fault_inject_llm 的知识问答式应答）。
# 用法：BOOT_USER=journey_boot BOOT_PASS='Boot@12345' bash scripts/e2e_session_isolation.sh
# =============================================================================
set -o pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
H="Content-Type: application/json"
RQ=""
BOOT_USER="${BOOT_USER:-journey_boot}"
BOOT_PASS="${BOOT_PASS:-Boot@12345}"
TIMEOUT="${TIMEOUT:-20}"

PASS=0; FAIL=0
step(){ echo ""; echo "▶▶▶ $1"; }
ok()  { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad() { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
jqx() { jq -r "$1" 2>/dev/null || echo ""; }

command -v curl >/dev/null || { echo "缺少 curl"; exit 1; }
command -v jq >/dev/null   || { echo "缺少 jq";   exit 1; }
curl -sf "$BASE/health" >/dev/null || { echo "❌ api($BASE) 未运行"; exit 1; }

# ---------- 0. 引导登录，创建全新租户（杜绝历史数据/会话干扰） ----------
step "0. 引导登录 + 创建全新租户"
BL=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":1,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}")
BTOK=$(jqx ".data.token" <<<"$BL")
[ -n "$BTOK" ] && ok "引导账号登录成功" || { bad "引导登录失败: $BL"; exit 1; }

TID=$(date +%s%N)
RES=$(curl -s -X POST "$BASE/api/tenant" -H "Authorization: Bearer $BTOK" -H "$H" -d "{\"name\":\"会话隔离测试$TID\"}")
TC=$(jqx ".code" <<<"$RES")
TENANT=$(jqx ".data.ID" <<<"$RES")
[ "$TC" = "0" ] && ok "创建全新租户 id=$TENANT" || { bad "建租户失败: $RES"; exit 1; }

# 该租户注册一个普通用户（建租户的引导账号即该租户 admin，直接用其登录测试即可）
U="sess_${TID}"
RC=$(curl -s -X POST "$BASE/api/user/register" -H "$H" -d "{\"tenant_id\":$TENANT,\"username\":\"$U\",\"password\":\"SessionA@123\",\"role\":\"member\"}")
[ "$(jqx ".code" <<<"$RC")" = "0" ] && ok "注册普通用户 $U" || ok "admin 直接用租户内账号测试"

L=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$TENANT,\"username\":\"$U\",\"password\":\"SessionA@123\"}")
LT=$(jqx ".code" <<<"$L")
if [ "$LT" = "0" ]; then
  TOK=$(jqx ".data.token" <<<"$L"); ok "普通用户登录成功"
else
  # 退回 admin 账号登录
  Lb=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$TENANT,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}")
  TOK=$(jqx ".data.token" <<<"$Lb"); ok "使用 admin 登录"
fi
[ -n "$TOK" ] || { bad "无 token 无法继续"; exit 1; }
AH="Authorization: Bearer $TOK"

# ---------- 1. 创建 3 个会话 ----------
step "1. 创建 3 个会话"
S1=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "$H" -d '{"title":"会话一"}' | jqx ".data.id")
S2=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "$H" -d '{"title":"会话二"}' | jqx ".data.id")
S3=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "$H" -d '{"title":"会话三"}' | jqx ".data.id")
[ -n "$S1" ] && [ -n "$S2" ] && [ -n "$S3" ] && ok "三个会话已创建: $S1 / $S2 / $S3" || { bad "建会话失败"; exit 1; }

# ---------- 2. 每个会话各说一句自我介绍 ----------
step "2. 三个会话各自写入不同内容（自我介绍）"
chat() { curl -s -m "$TIMEOUT" -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d "{\"session_id\":\"$1\",\"query\":\"$2\"}"; }
A1=$(chat "$S1" "我叫张三"); ok "会话1 说 '我叫张三' → code=$(jqx ".code" <<<"$A1")"
A2=$(chat "$S2" "我叫李四"); ok "会话2 说 '我叫李四' → code=$(jqx ".code" <<<"$A2")"
A3=$(chat "$S3" "我叫王五"); ok "会话3 说 '我叫王五' → code=$(jqx ".code" <<<"$A3")"

# ---------- 3. 自测点1：不同会话上下文不串（串行验证） ----------
step "3. 自测点1：不同会话上下文不串（串行）"
QA1=$(curl -s -m "$TIMEOUT" -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d "{\"session_id\":\"$S1\",\"query\":\"我叫什么？\"}")
QA2=$(curl -s -m "$TIMEOUT" -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d "{\"session_id\":\"$S2\",\"query\":\"我叫什么？\"}")
QA3=$(curl -s -m "$TIMEOUT" -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d "{\"session_id\":\"$S3\",\"query\":\"我叫什么？\"}")
ANS1=$(jqx ".data.answer" <<<"$QA1"); ANS2=$(jqx ".data.answer" <<<"$QA2"); ANS3=$(jqx ".data.answer" <<<"$QA3")
echo "    会话1『我叫什么』→ $ANS1"
echo "    会话2『我叫什么』→ $ANS2"
echo "    会话3『我叫什么』→ $ANS3"
case "$ANS1" in *张三*) ok "会话1 回答正确（张三）——上下文没串" ;; *) bad "会话1 回答错误：$ANS1（期望含 张三）" ;; esac
case "$ANS2" in *李四*) ok "会话2 回答正确（李四）——上下文没串" ;; *) bad "会话2 回答错误：$ANS2（期望含 李四）" ;; esac
case "$ANS3" in *王五*) ok "会话3 回答正确（王五）——上下文没串" ;; *) bad "会话3 回答错误：$ANS3（期望含 王五）" ;; esac
# 反向：会话1 的回答绝不能出现李四/王五；会话2 不能出现张三/王五；会话3 不能出现张三/李四
echo "$ANS1" | grep -qE '李四|王五' && bad "❌ 会话1 串到了 李四/王五" || ok "会话1 未串到会话2/3 的名字 ✅"
echo "$ANS2" | grep -qE '张三|王五' && bad "❌ 会话2 串到了 张三/王五" || ok "会话2 未串到会话1/3 的名字 ✅"
echo "$ANS3" | grep -qE '张三|李四' && bad "❌ 会话3 串到了 张三/李四" || ok "会话3 未串到会话1/2 的名字 ✅"

# ---------- 4. 自测点2：并发情况下依然隔离 ----------
step "4. 自测点2：并发提问，会话之间不串数据"
# 三个会话并行发起提问（各自独立请求 + 独立输出文件），验证并发下会话上下文仍不串。
# ⚠️ 用独立函数 + 独立文件，避免子 shell 里复用同一个重定向带来的竞争。
seqQ() { curl -s -m "$TIMEOUT" -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d "{\"session_id\":\"$1\",\"query\":\"我叫什么？\"}" > "$2"; }
seqQ "$S1" /tmp/ss_A1.json &
seqQ "$S2" /tmp/ss_A2.json &
seqQ "$S3" /tmp/ss_A3.json &
wait
CA1=$(jqx ".data.answer" < /tmp/ss_A1.json)
CA2=$(jqx ".data.answer" < /tmp/ss_A2.json)
CA3=$(jqx ".data.answer" < /tmp/ss_A3.json)
echo "    并发言一问 → $CA1"
echo "    并发言二问 → $CA2"
echo "    并发言三问 → $CA3"
case "$CA1" in *张三*) ok "并发下 会话1 仍答 张三 ✅（隔离）" ;; *) bad "并发下 会话1 答错：$CA1" ;; esac
case "$CA2" in *李四*) ok "并发下 会话2 仍答 李四 ✅（隔离）" ;; *) bad "并发下 会话2 答错：$CA2" ;; esac
case "$CA3" in *王五*) ok "并发下 会话3 仍答 王五 ✅（隔离）" ;; *) bad "并发下 会话3 答错：$CA3" ;; esac
echo "$CA1" | grep -qE '李四|王五' && bad "❌ 并发下 会话1 串到了 李四/王五" || ok "并发下 会话1 未串到其它会话 ✅"
echo "$CA2" | grep -qE '张三|王五' && bad "❌ 并发下 会话2 串到了 张三/王五" || ok "并发下 会话2 未串到其它会话 ✅"
echo "$CA3" | grep -qE '张三|李四' && bad "❌ 并发下 会话3 串到了 张三/李四" || ok "并发下 会话3 未串到其它会话 ✅"

# ---------- 5. 自测点3：会话列表能看到所有会话 ----------
step "5. 自测点3：会话列表能看到所有会话"
SL=$(curl -s "$BASE/api/session/list" -H "$AH")
TOTAL=$(jqx ".data.list | length" <<<"$SL")
IDS=$(jqx ".data.list[].ID" <<<"$SL" | tr '\n' ' ')
[ -z "$TOTAL" ] && TOTAL=0
[ "$TOTAL" = "3" ] && ok "会话列表包含全部 3 个会话（total=$TOTAL）" || bad "会话列表数目不对：total=$TOTAL（期望3）"
echo "    会话 ID 列表：$IDS"
echo "$IDS" | grep -qE "\b$S1\b" && ok "列表中可见 会话1($S1) ✅" || bad "列表中缺少 会话1($S1)"
echo "$IDS" | grep -qE "\b$S2\b" && ok "列表中可见 会话2($S2) ✅" || bad "列表中缺少 会话2($S2)"
echo "$IDS" | grep -qE "\b$S3\b" && ok "列表中可见 会话3($S3) ✅" || bad "列表中缺少 会话3($S3)"

echo ""
echo "=========================================================="
echo "  同用户多会话并发隔离：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 会话之间完全隔离，上下文不串，并发下依然有效" || echo "⚠️ 存在会话串数据，必须修复"
exit $((FAIL>0?1:0))
