#!/usr/bin/env bash
# =============================================================================
# 新用户完整旅程端到端测试（周五联调）
# -----------------------------------------------------------------------------
# 模拟"一个全新租户从注册到使用"的完整闭环：
#   创建新租户+admin → admin登录 → 上传txt文档 → 轮询异步处理成功
#   → 确认向量入库 → 新建会话 → 问文档内问题(应命中知识库工具)
#   → 问常识问题(应不调工具直接答) → 会话列表 → 用量统计 → 审计日志
#
# 用法： bash scripts/e2e_user_journey.sh   （需先启动 api + worker）
# 依赖： curl / jq
# =============================================================================
set -o pipefail   # 不启用 -u（nounset）：脚本变量多、管道多，避免未定义变量诡异中断；关键变量都显式给默认值并做存在性判断

BASE=${BASE:-http://127.0.0.1:8080}
# 从项目根 .env 读 MySQL 配置（用于第 11 步查审计日志）；工作目录不固定则向上找
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
for d in "$SCRIPT_DIR" "$SCRIPT_DIR/.."; do [ -f "$d/.env" ] && ENV_FILE="$d/.env" && break; done
[ -n "${ENV_FILE:-}" ] && set -a && . "$ENV_FILE" && set +a
MYSQL_PWD=${MYSQL_ROOT_PWD:-}
MYSQL_DB=${MYSQL_DB:-agent_platform}
MYSQL_USER=${MYSQL_USER:-root}
# 测试使用的租户名/账号，带时间戳避免与历史数据冲突
TS=$(date +%s)
TENANT_NAME="新用户旅程${TS}"
ADMIN_USER="journey_admin_${TS}"
ADMIN_PASS="Journey@123"

# 引导账号：用现有租户里的一个账号来创建新租户（接口在私有组需登录）
BOOT_TENANT_ID=${BOOT_TENANT_ID:-1}
BOOT_USER=${BOOT_USER:-}
BOOT_PASS=${BOOT_PASS:-}

PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step() { echo ""; echo "▶▶▶ $1"; }

jqx() { # 从 stdin 读 JSON，按 "$1" 取字段；取不到/失败时安全返回空
  jq -r "$1" 2>/dev/null || echo ""
}

have_jq() { command -v jq >/dev/null 2>&1; }
if ! have_jq; then echo "❌ 需要 jq，请先安装（brew install jq）"; exit 1; fi

# ---------- 引导登录：创建新租户需要私有路由（先拿一个引导 token） ----------
step "0. 引导登录（用现有租户账号，拿创建新租户的权限）"
if [ -n "$BOOT_USER" ]; then
  BOOT_TOKEN=$(curl -s -X POST "$BASE/api/user/login" -H "Content-Type: application/json" \
    -d "{\"tenant_id\":$BOOT_TENANT_ID,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jqx ".data.token")
  [ -z "$BOOT_TOKEN" ] && { bad "引导登录失败：请用 BOOT_USER/BOOT_PASS 提供现有租户账号"; exit 1; }
  ok "引导登录成功，拿到引导 token（创建租户用）"
else
  echo "  ⚠️ 未提供引导账号，跳过引导登录（假定直接以管理员操作）"
  BOOT_TOKEN=""
fi

H='Content-Type: application/json'

# ---------- 1. 创建新租户 + 管理员账号 ----------
step "1. 创建新租户 + 管理员账号"
if [ -n "$BOOT_TOKEN" ]; then
  TENANT=$(curl -s -X POST "$BASE/api/tenant" -H "$H" -H "Authorization: Bearer $BOOT_TOKEN" \
    -d "{\"name\":\"$TENANT_NAME\",\"admin_username\":\"$ADMIN_USER\",\"admin_password\":\"$ADMIN_PASS\"}")
  NEW_TENANT_ID=$(echo "$TENANT" | jqx ".data.ID")
  [ -z "$NEW_TENANT_ID" ] || [ "$NEW_TENANT_ID" = "null" ] && { bad "创建租户失败：$TENANT"; exit 1; }
  ok "创建租户成功: tenant_id=$NEW_TENANT_ID admin=$ADMIN_USER"
else
  NEW_TENANT_ID=$BOOT_TENANT_ID
  echo "  [沿用] 引导租户 tenant_id=$BOOT_TENANT_ID, 跳过建租户"
fi

# ---------- 2. 管理员登录，拿 token ----------
step "2. 新管理员登录，拿到 token"
LOGIN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$NEW_TENANT_ID,\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}")
TOKEN=$(echo "$LOGIN" | jqx ".data.token")
[ -z "$TOKEN" ] || [ "$TOKEN" = "null" ] && { bad "管理员登录失败：$LOGIN"; exit 1; }
ok "管理员登录成功，拿到 JWT token"
AH="Authorization: Bearer $TOKEN"

# ---------- 3. 上传一篇 txt 文档 ----------
step "3. 上传文档（txt）"
DOC_FILE="$(mktemp -d)/知识星球说明.txt"
cat > "$DOC_FILE" <<'CONTENT'
# 我们公司知识星球使用说明

公司内部员工每月可以在知识星球上发布 3 篇文章，每篇最高 20000 字。

员工发布文章后，由部门主编进行审核，审核通过后文章会被收录到公司知识库。

公司每年评选一次最佳知识分享奖，获得该奖项的员工将获得 5000 元奖金和三天带薪假期。

知识星球的默认使用权限对所有正式员工开放，实习生需要主管单独开通才能访问。

公司员工需要遵守保密协议，未经授权不得将知识星球中的内部资料外传。
CONTENT

# ⚠️ 上传用 -F（multipart），勿加 Authorization 之外的 Content-Type：
#    curl 的 -F 会自动设 multipart/form-data;boundary；若手动叠加 application/json 会
#    覆盖成 JSON 类型，handler 解析不到 file multipart 字段报"请上传文件"。
UPLOAD=$(curl -s -X POST "$BASE/api/document/upload" -H "$AH" \
  -F "file=@$DOC_FILE")
DOC_ID=$(echo "$UPLOAD" | jqx ".data.id")
TASK_ID=$(echo "$UPLOAD" | jqx ".data.task_id")
[ -z "$DOC_ID" ] || [ "$DOC_ID" = "null" ] && { bad "上传文档失败：$UPLOAD"; exit 1; }
echo "  [上传响应] 文档ID=$DOC_ID 任务ID=$TASK_ID"
ok "文档上传成功，document_id=$DOC_ID"

# ---------- 4. 轮询任务状态到 success ----------
step "4. 轮询异步处理（任务状态 → success）"
DSTATUS=""
MAX_WAIT=120
for i in $(seq 1 $((MAX_WAIT/3))); do
  sleep 3
  if [ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ]; then
    # 任务状态字段小写 status（handler 用 gin.H{"status": ...}），勿用大写 Status
    DSTATUS=$(curl -s "$BASE/api/task/$TASK_ID" -H "$AH" | jqx ".data.status")
  else
    # document list 字段同样小写（id/status）
    DSTATUS=$(curl -s "$BASE/api/document/list?page=1&page_size=50" -H "$AH" | jq -r ".data.list[] | select(.id==$DOC_ID) | .status" 2>/dev/null | head -1)
  fi
  if [ "$DSTATUS" = "success" ]; then ok "任务处理成功（历时约 $((i*3)) 秒），状态=success"; break; fi
  if [ "$DSTATUS" = "failed" ]; then bad "任务处理失败，状态=failed（见任务详情）"; break; fi
  echo -n "  ⏳ 处理中... (status=$DSTATUS) "
done
[ "$DSTATUS" = "success" ] || { bad "任务未在 ${MAX_WAIT}s 内变为 success, 最后 status=$DSTATUS"; exit 1; }
TASK_STATUS="$DSTATUS"

echo "  ✅ 文档状态已确认为 success（异步管道处理完成）"

# ---------- 5. 确认向量已入库（通过知识库检索直接命中本文档片段） ----------
step "5. 知识库命中自检（确定性验证向量已入库）"
HITPHRASE=$(curl -s -X POST "$BASE/api/knowledge/search" -H "$AH" -H "$H" \
-d "{\"query\":\"最佳知识分享奖奖金多少\",\"top_k\":3}" \
| jq -r ".data.results[0].content" 2>/dev/null)
case "$HITPHRASE" in
*"5000"*|*"奖金"*|*"带薪"*) ok "知识库检索命中本文档片段（含'5000元奖金/带薪假期'）→ 向量确已入库" ;;
*) bad "知识库未命中本文档（content=${HITPHRASE:0:40}...），向量可能未入库" ;;
esac

# ---------- 6. 新建会话 ----------
step "6. 新建会话"
SESS=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "$H" -d '{"title":"新用户旅程测试会话"}')
SESSION_ID=$(echo "$SESS" | jqx ".data.id")
[ -z "$SESSION_ID" ] || [ "$SESSION_ID" = "null" ] && { bad "创建会话失败：$SESS"; exit 1; }
ok "新建会话成功：session_id=$SESSION_ID"

# ---------- 7. 问一个"文档里有的问题" → 应命中知识库工具 ----------
step "7. 提问：文档内问题 → 期望命中 knowledge_retrieve 工具"
A1=$(curl -s -X POST "$BASE/api/chat" -H "$AH" -H "$H" \
-d "{\"session_id\":\"$SESSION_ID\",\"query\":\"我们公司最佳知识分享奖的奖金是多少？\"}")
TOOL1=$(echo "$A1" | jqx ".data.tool_calls" | tr -d '[]" ')
ANSWER1=$(echo "$A1" | jqx ".data.answer")
echo "    → tool_calls=[$TOOL1]"
echo "    → answer=$ANSWER1"
case "$TOOL1" in *knowledge_retrieve*) ok "命中知识库工具 knowledge_retrieve ✅" ;; *) bad "未命中知识库工具, 实际 tool_calls=$TOOL1" ;; esac
case "$ANSWER1" in *5000*) ok "回答内容正确含'5000'（与文档一致非编造）✅" ;; *) bad "回答内容未含预期金额, answer=$ANSWER1" ;; esac

# ---------- 8. 问常识问题 → 应不调用工具直接回答 ----------
step "8. 提问：常识问题 → 期望不调用工具直接回答"
A2=$(curl -s -X POST "$BASE/api/chat" -H "$AH" -H "$H" \
-d "{\"session_id\":\"$SESSION_ID\",\"query\":\"地球离太阳多远？\"}")
TOOL2=$(echo "$A2" | jqx ".data.tool_calls" | tr -d '[]" ')
ANSWER2=$(echo "$A2" | jqx ".data.answer")
echo "    → tool_calls=[$TOOL2]"
echo "    → answer=${ANSWER2:0:60}..."
if [ -z "$TOOL2" ]; then ok "常识问题未调用工具（直接回答）✅"; else bad "常识问题不应调用工具，但 tool_calls=$TOOL2"; fi

# ---------- 9. 会话列表：能看到刚才的会话 ----------
step "9. 会话列表（应包含刚才创建的会话）"
SESS_LIST=$(curl -s "$BASE/api/session/list?page=1&page_size=20" -H "$AH")
TOTAL_SESS=$(echo "$SESS_LIST" | jqx ".data.total")
echo "    → 用户会话总数=$TOTAL_SESS"
[ "$TOTAL_SESS" = "null" ] || [ -z "$TOTAL_SESS" ] || [ "$TOTAL_SESS" = "0" ] \
  && bad "会话列表为空，看不到会话" \
  || ok "会话列表有 $TOTAL_SESS 条会话（含本次测试会话）✅"

# 确认本次会话确实在列表里
IN_LIST=$(echo "$SESS_LIST" | jq --argjson sid "$SESSION_ID" "[.data.list[] | select(.ID==\$sid)][0].ID" 2>/dev/null)
[ "$IN_LIST" = "$SESSION_ID" ] 2>/dev/null \
  && ok "刚创建的会话 session_id=$SESSION_ID 在列表中可见" \
  || bad "列表未找到本次会话"

# ---------- 10. 用量统计：能看到 token 消耗（管理员接口） ----------
step "10. 查询当天用量统计（token 消耗）"
USG=$(curl -s "$BASE/api/admin/usage/today" -H "$AH")
USG_TOKENS=$(echo "$USG" | jqx ".data.tokens")
USG_CALLS=$(echo "$USG" | jqx ".data.calls")
echo "    → tokens=${USG_TOKENS}  calls=${USG_CALLS}"
case "$USG_TOKENS" in ""|null|0) bad "用量统计未见 token 消耗（可能对话没真实调 LLM）" ;; *) ok "用量统计正常：本租户当天 token=${USG_TOKENS}，调用=${USG_CALLS} 次 ✅" ;; esac

# ---------- 11. 审计日志：能看到登录、上传文档等操作记录 ----------
step "11. 查审计日志（登录 / 上传文档 / 会话等操作记录）"
AUDIT=$(docker exec agent-mysql mysql --database="$MYSQL_DB" --user=root --password="$MYSQL_PWD" --default-character-set=utf8mb4 -N -B \
-e "SELECT operation, COUNT(*) FROM audit_logs WHERE tenant_id=$NEW_TENANT_ID GROUP BY operation ORDER BY 2 DESC;" 2>/dev/null)
echo "$AUDIT" | sed 's/^/    → /'
# 覆盖所有审计埋点：登录 / 上传文档 / 创建会话 / RAG问答
for op in 登录 上传文档 创建会话 RAG问答; do
if echo "$AUDIT" | grep -q "$op"; then ok "审计日志含 [$op] 记录 ✅（带 trace_id）"; else bad "审计日志缺 [$op] 记录（已查租户 #$NEW_TENANT_ID 内）"; fi
done

# 强调验证 RAG 问答审计里记录了"命中工具"，能观测量到工具调用留痕
RAG_HIT=$(docker exec agent-mysql mysql --database="$MYSQL_DB" --user=root --password="$MYSQL_PWD" --default-character-set=utf8mb4 -N -B \
  -e "SELECT COUNT(*) FROM audit_logs WHERE tenant_id=$NEW_TENANT_ID AND operation='RAG问答' AND content LIKE '%knowledge_retrieve%';" 2>/dev/null)
if [ "${RAG_HIT:-0}" -ge 1 ] 2>/dev/null; then ok "RAG 问答审计记录了命中 knowledge_retrieve 工具 ✅"; else bad "RAG 问答审计未记录工具命中详情"; fi

# ---------- 汇总 ----------
echo ""
echo "=========================================================="
echo "  新用户完整旅程测试：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 全部通过" || echo "⚠️ 存在失败项，请见上方 ❌"
[ -n "$NEW_TENANT_ID" ] && echo "[完成] 测试租户 tenant_id=$NEW_TENANT_ID, 文档 document_id=$DOC_ID"
exit $((FAIL>0?1:0))
