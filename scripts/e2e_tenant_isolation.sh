#!/usr/bin/env bash
# =============================================================================
# e2e_tenant_isolation.sh —— 多租户并发隔离测试（核心中的核心）
# -----------------------------------------------------------------------------
# 场景：
#   准备全新租户 A、租户 B，各上传含"独家关键词"的机密文档：
#     A: "苹果香蕉梨 123456"
#     B: "橘子葡萄西瓜 789012"
#   同时用两个账号并发提问：
#     A 问 "橘子葡萄西瓜是什么"
#     B 问 "苹果香蕉梨是什么"
#   验证：A 搜不到 B 的向量 / B 搜不到 A 的向量（并发+非并发都成立）。
#
#   再测越权操作：
#     B 拿 A 的文档 ID → 查详情 / 删除 → 失败
#     B 拿 A 的会话 ID → 查历史 / 删除 → 失败
#   预期：严格隔离，无任何数据越权。
#
# 自测点：
#   1. 租户 A 搜不到租户 B 的向量
#   2. 租户 B 搜不到租户 A 的向量
#   3. 越权查文档失败
#   4. 越权删文档失败
#   5. 越权查会话失败
#   6. 并发情况下隔离依然有效
#
# 依赖：curl / jq；api + worker 已启动；mock/真实 LLM 就绪。
# 用法：bash scripts/e2e_tenant_isolation.sh
#   （引导创建租户需 BOOT_TENANT_ID/BOOT_USER/BOOT_PASS 提供现有账号）
# =============================================================================
set -o pipefail   # 不用 -u：脚本变量多，避免未定义变量诡异中断；关键变量都判断

BASE=${BASE:-http://127.0.0.1:8080}
BOOT_TENANT_ID=${BOOT_TENANT_ID:-1}
BOOT_USER=${BOOT_USER:-}
BOOT_PASS=${BOOT_PASS:-}

TS=$(date +%s)
TENANT_A="租户A隔离${TS}"
TENANT_B="租户B隔离${TS}"
USER_A="iso_a_${TS}"
USER_B="iso_b_${TS}"
PASS_A="IsolationA@123"
PASS_B="IsolationB@123"

PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step() { echo ""; echo "▶▶▶ $1"; }
jqx()  { jq -r "$1" 2>/dev/null || echo ""; }
[ "$(command -v jq)" ] || { echo "❌ 需要 jq（brew install jq）"; exit 1; }
H='Content-Type: application/json'

# ---------- 0. 引导登录（建租户需要私有路由） ----------
step "0. 引导登录（现有账号，用于创建租户 A/B）"
[ -n "$BOOT_USER" ] || { echo "❌ 请提供 BOOT_USER/BOOT_PASS 引导账号（当前租户可创建租户的账号）"; exit 1; }
BOOT_TOKEN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$BOOT_TENANT_ID,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jqx ".data.token")
[ -n "$BOOT_TOKEN" ] && [ "$BOOT_TOKEN" != "null" ] || { bad "引导登录失败"; exit 1; }
ok "引导登录成功（建租户用）"
BTOKEN="Authorization: Bearer $BOOT_TOKEN"

# ---------- 1. 创建租户 A / B 及各自管理员 ----------
step "1. 创建租户 A 与租户 B（互不干扰）"
TA=$(curl -s -X POST "$BASE/api/tenant" -H "$H" -H "$BTOKEN" -d "{\"name\":\"$TENANT_A\",\"admin_username\":\"$USER_A\",\"admin_password\":\"$PASS_A\"}")
TAID=$(echo "$TA" | jqx ".data.ID")
TB=$(curl -s -X POST "$BASE/api/tenant" -H "$H" -H "$BTOKEN" -d "{\"name\":\"$TENANT_B\",\"admin_username\":\"$USER_B\",\"admin_password\":\"$PASS_B\"}")
TBID=$(echo "$TB" | jqx ".data.ID")
[ -n "$TAID" ] && [ "$TAID" != "null" ] && [ -n "$TBID" ] && [ "$TBID" != "null" ] \
  || { bad "创建租户失败：A=$TA B=$TB"; exit 1; }
ok "租户 A id=$TAID / 租户 B id=$TBID（全新租户，杜绝历史数据干扰）"

# ---------- 2. 两管理员分别登录 ----------
step "2. 租户 A / B 管理员登录"
LOGIN_A=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$TAID,\"username\":\"$USER_A\",\"password\":\"$PASS_A\"}")
TOKEN_A=$(echo "$LOGIN_A" | jqx ".data.token")
LOGIN_B=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$TBID,\"username\":\"$USER_B\",\"password\":\"$PASS_B\"}")
TOKEN_B=$(echo "$LOGIN_B" | jqx ".data.token")
[ -n "$TOKEN_A" ] && [ -n "$TOKEN_B" ] || { bad "登录失败"; exit 1; }
AH="Authorization: Bearer $TOKEN_A"
BH="Authorization: Bearer $TOKEN_B"
ok "租户 A 管理员 token ✅ / 租户 B 管理员 token ✅"

# ---------- 3. 各上传机密文档 ----------
step "3. 上传机密文档（A 含'苹果香蕉梨 123456'，B 含'橘子葡萄西瓜 789012'）"
DIR_A=$(mktemp -d); DIR_B=$(mktemp -d)
cat > "$DIR_A/机密A.txt" <<'EOT'
# 机密档案-租户A
本租户的核心机密内容如下：苹果香蕉梨 123456。
这些信息仅属于租户 A，严禁外泄给其他租户。
EOT
cat > "$DIR_B/机密B.txt" <<'EOT'
# 机密档案-租户B
本租户的核心机密内容如下：橘子葡萄西瓜 789012。
这些信息仅属于租户 B，严禁外泄给其他租户。
EOT
UP_A=$(curl -s -X POST "$BASE/api/document/upload" -H "$AH" -F "file=@$DIR_A/机密A.txt")
DOC_A=$(echo "$UP_A" | jqx ".data.id")
TASK_A=$(echo "$UP_A" | jqx ".data.task_id")
UP_B=$(curl -s -X POST "$BASE/api/document/upload" -H "$BH" -F "file=@$DIR_B/机密B.txt")
DOC_B=$(echo "$UP_B" | jqx ".data.id")
TASK_B=$(echo "$UP_B" | jqx ".data.task_id")
[ -n "$DOC_A" ] && [ "$DOC_A" != "null" ] || { bad "上传 A 失败：$UP_A"; exit 1; }
[ -n "$DOC_B" ] && [ "$DOC_B" != "null" ] || { bad "上传 B 失败：$UP_B"; exit 1; }
ok "文档已上传：A id=$DOC_A（任务$TASK_A） / B id=$DOC_B（任务$TASK_B）"

# ---------- 4. 轮询两份文档向量化到 success ----------
step "4. 轮询异步处理到 success（确保向量已入库）"
wait_doc() { # $1=任务ID $2=租户token $3=标签
  local tid=$1 tok=$2 tag=$3 st=""
  for i in $(seq 1 40); do
    sleep 3
    st=$(curl -s "$BASE/api/task/$tid" -H "$tok" | jqx ".data.status")
    [ "$st" = "success" ] || [ "$st" = "failed" ] && break
  done
  echo "$st"
}
SAT=$(wait_doc "$TASK_A" "$AH" A)
SBT=$(wait_doc "$TASK_B" "$BH" B)
[ "$SAT" = "success" ] && [ "$SBT" = "success" ] \
  && ok "两份文档向量化均 success（A=$SAT B=$SBT）" \
  || { bad "向量化未成功：A=$SAT B=$SBT"; exit 1; }

# ksearch：调知识库检索，返回命中片段 content 拼接
ksearch() { # $1=token header  $2=query
  curl -s -X POST "$BASE/api/knowledge/search" -H "$1" -H "$H" -d "{\"query\":\"$2\",\"top_k\":5}" \
    | jq -r '.data.results[]?.content' 2>/dev/null
}

# ---------- 5. 确定性子验证：各搜自己的机密词能命中 ----------
step "5. 确定性验证：A/B 各能搜到自己的机密（向量确已隔离入库）"
HA=$(ksearch "$AH" "苹果香蕉梨 123456")
HB=$(ksearch "$BH" "橘子葡萄西瓜 789012")
case "$HA" in *苹果香蕉梨*|*123456*) ok "A 能搜到自己的'苹果香蕉梨 123456' ✅" ;; *) bad "A 搜不到自己内容：$HA" ;; esac
case "$HB" in *橘子葡萄西瓜*|*789012*) ok "B 能搜到自己的'橘子葡萄西瓜 789012' ✅" ;; *) bad "B 搜不到自己内容：$HB" ;; esac

# ---------- 6. 核心：A 搜不到 B 的向量，B 搜不到 A 的向量 ----------
step "6. 双向隔离验证（A 搜不到 B / B 搜不到 A 的向量）"
# A 用 B 的独家词检索 → 应完全没有 B 的机密切片
AB=$(ksearch "$AH" "橘子葡萄西瓜 789012")
if echo "$AB" | grep -qE '橘子葡萄西瓜|789012'; then bad "❌ 租户 A 搜到了租户 B 的机密（泄漏！）"; else ok "A 搜'橘子葡萄西瓜 789012' 搜不到 B 内容 ✅（隔离生效）"; fi
# B 用 A 的独家词检索 → 应完全没有 A 的机密切片
BA=$(ksearch "$BH" "苹果香蕉梨 123456")
if echo "$BA" | grep -qE '苹果香蕉梨|123456'; then bad "❌ 租户 B 搜到了租户 A 的机密（泄漏！）"; else ok "B 搜'苹果香蕉梨 123456' 搜不到 A 内容 ✅（隔离生效）"; fi

# ---------- 7. 并发提问验证隔离 ----------
step "7. 并发提问（A 问 B 的词 / B 问 A 的词）——验证并发下隔离有效"
qa() { curl -s -m 45 -X POST "$BASE/api/chat" -H "$1" -H "$H" -d "{\"query\":\"$2\"}" ; }
# 并发后台执行
qa "$AH" "橘子葡萄西瓜是什么，具体内容？" > /tmp/iso_A_ans.json &
PID_A=$!
qa "$BH" "苹果香蕉梨是什么，具体内容？" > /tmp/iso_B_ans.json &
PID_B=$!
wait $PID_A; wait $PID_B
ANS_A=$(cat /tmp/iso_A_ans.json | jqx ".data.answer")
ANS_B=$(cat /tmp/iso_B_ans.json | jqx ".data.answer")
CODE_A=$(cat /tmp/iso_A_ans.json | jqx ".code")
CODE_B=$(cat /tmp/iso_B_ans.json | jqx ".code")
echo "    → A 并发 chat code=$CODE_A, answer: ${ANS_A:0:60}"
echo "    → B 并发 chat code=$CODE_B, answer: ${ANS_B:0:60}"
# A 的回答若出现 B 的机密词 → 泄漏；B 的回答出现 A 的机密词 → 泄漏
if echo "$ANS_A" | grep -qE '橘子葡萄西瓜|789012'; then bad "❌ 并发下 A 的回答泄露了 B 的机密（$ANS_A）"; else ok "A 并发提问未泄露 B 的机密 ✅"; fi
if echo "$ANS_B" | grep -qE '苹果香蕉梨|123456'; then bad "❌ 并发下 B 的回答泄露了 A 的机密（$ANS_B）"; else ok "B 并发提问未泄露 A 的机密 ✅"; fi

# ---------- 8. 越权查文档 / 删文档（B 拿 A 的 doc_id） ----------
step "8. 越权文档操作（B 拿 A 的文档 ID）"
RD=$(curl -s "$BASE/api/document/$DOC_A" -H "$BH")   # B 查 A 文档详情
RCODE=$(echo "$RD" | jqx ".code")
[ "$RCODE" = "0" ] && bad "❌ B 查到了 A 的文档（越权！$RD）" || ok "B 查 A 文档被拒（code=$RCODE '$(echo "$RD"|jqx .message)'）✅"

DD=$(curl -s -X DELETE "$BASE/api/document/$DOC_A" -H "$BH")  # B 删 A 文档
DCODE=$(echo "$DD" | jqx ".code")
[ "$DCODE" = "0" ] && bad "❌ B 删掉了 A 的文档（越权！$DD）" || ok "B 删 A 文档被拒（code=$DCODE '$(echo "$DD"|jqx .message)'）✅"

# 确认 A 自己的文档还在（未被误删，B 的越权删确实没成功）
RA=$(curl -s "$BASE/api/document/$DOC_A" -H "$AH")
[ "$(echo "$RA" | jqx .code)" = "0" ] && ok "A 自己的文档完好（越权删除未生效）✅" || bad "A 文档异常"

# ---------- 9. 会话隔离：B 拿 A 的会话 ID 查历史 / 删除 ----------
step "9. 越权会话操作（B 拿 A 的会话 ID）"
# A 创建会话并问一句产生历史
SESS_A=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "$H" -d '{"title":"A的机密会话"}')
SID_A=$(echo "$SESS_A" | jqx ".data.id")
curl -s -m 45 -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d "{\"session_id\":$SID_A,\"query\":\"你好，记住这是租户A的私密对话，苹果香蕉梨\"}" >/dev/null
# B 也建会话，供对照
SESS_B=$(curl -s -X POST "$BASE/api/session" -H "$BH" -H "$H" -d '{"title":"B的会话"}')
SID_B=$(echo "$SESS_B" | jqx ".data.id")

RH=$(curl -s "$BASE/api/session/$SID_A/messages" -H "$BH")  # B 查 A 会话历史
RHCODE=$(echo "$RH" | jqx ".code")
[ "$RHCODE" = "0" ] && bad "❌ B 读到了 A 的会话历史（越权！$RH）" || ok "B 查 A 会话历史被拒（code=$RHCODE '$(echo "$RH"|jqx .message)'）✅"

DSH=$(curl -s -X DELETE "$BASE/api/session/$SID_A" -H "$BH")  # B 删 A 会话
DSHCODE=$(echo "$DSH" | jqx ".code")
[ "$DSHCODE" = "0" ] && bad "❌ B 删掉了 A 的会话（越权！$DSH）" || ok "B 删 A 会话被拒（code=$DSHCODE '$(echo "$DSH"|jqx .message)'）✅"

# 对照：A 查自己的会话历史应成功；B 查自己的也应成功
RHA=$(curl -s "$BASE/api/session/$SID_A/messages" -H "$AH")
[ "$(echo "$RHA" | jqx .code)" = "0" ] \
  && ok "A 查自己会话历史正常（${#RHA} 字节，能看到历史）✅" \
  || bad "A 查自己会话历史异常：$RHA"
RHB=$(curl -s "$BASE/api/session/$SID_B/messages" -H "$BH")
[ "$(echo "$RHB" | jqx .code)" = "0" ] && ok "B 查自己会话历史正常 ✅" || bad "B 查自己会话历史异常：$RHB"

# ---------- 汇总 ----------
echo ""
echo "=========================================================="
echo "  多租户并发隔离测试：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 严格隔离，无任何数据越权" || echo "⚠️ 存在越权/泄漏，必须修复"
exit $((FAIL>0?1:0))
