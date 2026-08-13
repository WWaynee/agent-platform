#!/usr/bin/env bash
# =============================================================================
# 管理功能联调端到端测试
# -----------------------------------------------------------------------------
# 验证租户管理员的管理功能与权限控制：
#   建租户+admin → admin登录 → 上传文档并等成功
#   → 查看工具配置列表 → 关闭知识库工具 → 问文档问题(应无文档事实"未启用")
#   → 重新开启知识库工具 → 再问(应恢复命中文档事实)
#   → 查看租户当天用量 → 查看任务列表(能看到文档处理任务)
#   → 权限：普通成员调管理接口→403；管理接口与普通接口对比
#
# 用法： bash scripts/e2e_admin_mgmt.sh   （需先启动 api + worker）
# 依赖： curl / jq
# =============================================================================
set -o pipefail

BASE=${BASE:-http://127.0.0.1:8080}
# 从项目根 .env 读 MySQL 配置（用于查审计/任务验证）；向上找工作目录
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
for d in "$SCRIPT_DIR" "$SCRIPT_DIR/.."; do [ -f "$d/.env" ] && ENV_FILE="$d/.env" && break; done
[ -n "${ENV_FILE:-}" ] && set -a && . "$ENV_FILE" && set +a
MYSQL_PWD=${MYSQL_ROOT_PWD:-}
MYSQL_DB=${MYSQL_DB:-agent_platform}

# 测试用的租户名 / 管理员 / 普通成员账号（带时间戳避免历史冲突）
TS=$(date +%s)
TENANT_NAME="管理联调${TS}"
ADMIN_USER="mgr_admin_${TS}"
ADMIN_PASS="Mgr@12345"
MEMBER_USER="mgr_member_${TS}"
MEMBER_PASS="Mem@12345"

BOOT_TENANT_ID=${BOOT_TENANT_ID:-1}
BOOT_USER=${BOOT_USER:-}
BOOT_PASS=${BOOT_PASS:-}

PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step() { echo ""; echo "▶▶▶ $1"; }

jqx() { jq -r "$1" 2>/dev/null || echo ""; }
command -v jq >/dev/null 2>&1 || { echo "❌ 需要 jq（brew install jq）"; exit 1; }

H='Content-Type: application/json'
FACT="8000"            # 文档里埋的事实金额（用于判断"能否检索到内部知识"）
DOC_KEYWORD="内部培训" # 文档里的一个关键词

# ---------- 准备：引导登录并建租户（拿到 admin 与 member 两个账号） ----------
step "0. 引导登录（用现有租户账号，建新租户）"
BOOT_TOKEN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$BOOT_TENANT_ID,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jqx ".data.token")
[ -z "$BOOT_TOKEN" ] && { bad "引导登录失败（需 BOOT_USER/BOOT_PASS 提供现有租户账号）"; exit 1; }
ok "引导登录成功，拿到引导 token"

step "1. 创建新租户（自动建 admin 管理员）"
TENANT=$(curl -s -X POST "$BASE/api/tenant" -H "$H" -H "Authorization: Bearer $BOOT_TOKEN" \
  -d "{\"name\":\"$TENANT_NAME\",\"admin_username\":\"$ADMIN_USER\",\"admin_password\":\"$ADMIN_PASS\"}")
TID=$(echo "$TENANT" | jqx ".data.ID")
[ -z "$TID" ] || [ "$TID" = "null" ] && { bad "创建租户失败：$TENANT"; exit 1; }
ok "创建租户成功: tenant_id=$TID admin=$ADMIN_USER"

step "2. 管理员登录，拿到 JWT token"
LOGIN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}")
TOKEN=$(echo "$LOGIN" | jqx ".data.token")
[ -z "$TOKEN" ] || [ "$TOKEN" = "null" ] && { bad "管理员登录失败：$LOGIN"; exit 1; }
ok "管理员登录成功"
AH="Authorization: Bearer $TOKEN"

# ---------- 3. 上传一篇含管理可检索事实的文档，等异步解析成功 ----------
step "3. 上传文档（txt）并等它处理成功"
DOC_FILE="$(mktemp -d)/mgr_doc.txt"
cat > "$DOC_FILE" <<CONTENT
我们公司的最佳知识分享奖奖金为 $FACT 元，获奖员工可获得三天带薪假期。
公司的$DOC_KEYWORD资料只有正式员工可以查阅。
CONTENT
UPLOAD=$(curl -s -X POST "$BASE/api/document/upload" -H "$AH" -F "file=@$DOC_FILE")
DOC_ID=$(echo "$UPLOAD" | jqx ".data.id")
TASK_ID=$(echo "$UPLOAD" | jqx ".data.task_id")
[ -z "$DOC_ID" ] || [ "$DOC_ID" = "null" ] && { bad "上传文档失败：$UPLOAD"; exit 1; }
echo "  [上传响应] 文档ID=$DOC_ID 任务ID=$TASK_ID"
DSTATUS=""
for i in $(seq 1 40); do
  sleep 3
  DSTATUS=$(curl -s "$BASE/api/task/$TASK_ID" -H "$AH" | jqx ".data.status")
  [ "$DSTATUS" = "success" ] && break
  [ "$DSTATUS" = "failed" ] && break
done
[ "$DSTATUS" = "success" ] && ok "文档处理成功（任务 $TASK_ID → success）" || { bad "文档处理未成功（status=$DSTATUS）"; exit 1; }

# ============ 面板一：工具开关生效 ============
step "4. 查看工具配置列表（管理员）"
TCLIST=$(curl -s "$BASE/api/admin/tool-config" -H "$AH")
TOOLS_JSON=$(echo "$TCLIST" | jqx ".data")
echo "  → $(echo "$TCLIST" | jq -c '.data.list[] | {tool_name, is_enable}' 2>/dev/null | tr '\n' ' ')"
case "$TCLIST" in *knowledge_retrieve*) ok "工具配置列表可见（含 knowledge_retrieve）✅" ;; *) bad "工具配置列表异常：$TCLIST" ;; esac

step "5. 关闭知识库工具 knowledge_retrieve"
CLOSE=$(curl -s -X PUT "$BASE/api/admin/tool-config/knowledge_retrieve" -H "$AH" -H "$H" -d '{"is_enable":false}')
ENABLED_NOW=$(curl -s "$BASE/api/admin/tool-config" -H "$AH" | jqx ".data.list[] | select(.tool_name==\"knowledge_retrieve\") | .is_enable")
[ "$ENABLED_NOW" = "false" ] && ok "知识库工具已关闭（is_enable=false）✅" || { bad "工具未关闭：$CLOSE / is_enable=$ENABLED_NOW"; }

step "6. 工具关闭后，问文档里的问题 → 应无文档事实（工具未启用）"
# 用新会话问，避免旧会话 Redis 历史里的上下文混入文档事实
A_OFF=$(curl -s -X POST "$BASE/api/chat" -H "$AH" -H "$H" \
  -d "{\"session_id\":\"\",\"query\":\"我们公司最佳知识分享奖的奖金是多少？\"}")
ANS_OFF=$(echo "$A_OFF" | jqx ".data.answer")
TOOL_OFF=$(echo "$A_OFF" | jqx ".data.tool_calls" | tr -d '[]" ')
echo "    → tool_calls=[$TOOL_OFF]"
echo "    → answer=$ANS_OFF"
if echo "$ANS_OFF" | grep -q "$FACT"; then
  bad "工具已关闭却仍答出了 $FACT 元（权限拦截未生效？）"
else
  ok "关闭后回答不含文档事实 $FACT 元（工具确已禁用拦截）✅"
fi
case "$ANS_OFF" in *未启用*|*无法*|*不能*|*无权*|*内部知识*) ok "回答明确表达'工具未启用/无法查询内部知识'语义 ✅" ;; *) bad "回答未体现'未启用'语义：$ANS_OFF" ;; esac

step "7. 重新开启知识库工具"
curl -s -X PUT "$BASE/api/admin/tool-config/knowledge_retrieve" -H "$AH" -H "$H" -d '{"is_enable":true}' >/dev/null
ENABLED_NOW=$(curl -s "$BASE/api/admin/tool-config" -H "$AH" | jqx ".data.list[] | select(.tool_name==\"knowledge_retrieve\") | .is_enable")
[ "$ENABLED_NOW" = "true" ] && ok "知识库工具已重新开启（is_enable=true）✅" || bad "工具未重新开启，is_enable=$ENABLED_NOW"

step "8. 重新开启后，再问文档里的问题 → 应恢复命中文档事实"
A_ON=$(curl -s -X POST "$BASE/api/chat" -H "$AH" -H "$H" \
  -d "{\"session_id\":\"\",\"query\":\"我们公司最佳知识分享奖的奖金是多少？\"}")
ANS_ON=$(echo "$A_ON" | jqx ".data.answer")
TOOL_ON=$(echo "$A_ON" | jqx ".data.tool_calls" | tr -d '[]" ')
echo "    → tool_calls=[$TOOL_ON]"
echo "    → answer=$ANS_ON"
case "$ANS_ON" in *"$FACT"*) ok "回答正确恢复命中文档事实 $FACT 元（工具开关生效）✅" ;; *) bad "开启后仍未检索到 $FACT：$ANS_ON" ;; esac

# ============ 面板二：用量 / 任务查询 ============
step "9. 查看租户当天用量（管理员）"
USG=$(curl -s "$BASE/api/admin/usage/today" -H "$AH")
USG_T=$(echo "$USG" | jqx ".data.tokens")
USG_C=$(echo "$USG" | jqx ".data.calls")
echo "    → tokens=$USG_T  calls=$USG_C"
case "$USG_T" in ""|null|0) bad "用量统计异常/为 0" ;; *) ok "租户当天用量正常: token=$USG_T 调用=$USG_C 次 ✅" ;; esac

step "10. 查看任务列表（应能看到文档处理任务）"
TSK=$(curl -s "$BASE/api/admin/task/list?page=1&page_size=20" -H "$AH")
TOTAL_TASK=$(echo "$TSK" | jqx ".data.total")
echo "    → 任务总数=$TOTAL_TASK"
if echo "$TSK" | jq -e '.data.list[] | select(.id=='"$TASK_ID"' and .biz_id=='"$DOC_ID"')' >/dev/null 2>&1; then
  ok "任务列表能看到文档处理任务（task=$TASK_ID, biz=$DOC_ID, status=success）✅"
else
  bad "任务列表未找到文档处理任务：$TSK"
fi

# ============ 面板三：权限控制 ============
step "11. 普通成员调管理接口 → 应 403 拒绝"
MREG=$(curl -s -X POST "$BASE/api/user/register" -H "$AH" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"$MEMBER_USER\",\"password\":\"$MEMBER_PASS\",\"role\":\"member\"}")
MLOGIN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"$MEMBER_USER\",\"password\":\"$MEMBER_PASS\"}")
MTOKEN=$(echo "$MLOGIN" | jqx ".data.token")
[ -z "$MTOKEN" ] || [ "$MTOKEN" = "null" ] && { bad "普通成员登录失败：$MLOGIN"; exit 1; }
MAH="Authorization: Bearer $MTOKEN"
DENIED=1
for EP in "$BASE/api/admin/tool-config" "$BASE/api/admin/usage/today" "$BASE/api/admin/task/list"; do
  R=$(curl -s "$EP" -H "$MAH")
  C=$(echo "$R" | jqx ".code")
  [ "$C" = "403" ] && continue
  DENIED=0; bad "普通成员访问 $EP 未被拒绝（code=$C）: $R"
done
[ "$DENIED" -eq 1 ] && ok "普通成员访问 工具配置/用量/任务列表 均被拒绝（403 仅管理员可操作）✅"

step "12. 权限对照：普通成员普通接口正常 / 管理接口被拒"
DOCLIST=$(curl -s "$BASE/api/document/list" -H "$MAH")
DLC=$(echo "$DOCLIST" | jqx ".code")
[ "$DLC" = "0" ] && ok "普通成员可正常访问文档列表（code=0）✅" || bad "普通成员访问文档列表异常：$DOCLIST"
# 普通成员尝试开关工具（管理接口）应被拒
RM=$(curl -s -X PUT "$BASE/api/admin/tool-config/knowledge_retrieve" -H "$MAH" -H "$H" -d '{"is_enable":false}')
[ "$(echo "$RM" | jqx '.code')" = "403" ] && ok "普通成员不能改工具配置（403）✅" || bad "普通成员竟能改工具配置？$RM"
# 确认工具开关未被普通成员改掉（仍为 true，因为上一步被拒）
ENABLED_FINAL=$(curl -s "$BASE/api/admin/tool-config" -H "$AH" | jqx ".data.list[] | select(.tool_name==\"knowledge_retrieve\") | .is_enable")
[ "$ENABLED_FINAL" = "true" ] && ok "工具配置未被子成员请求误改（is_enable 保持 true）✅" || bad "工具配置被意外改动：$ENABLED_FINAL"

# ---------- 汇总 ----------
echo ""
echo "=========================================================="
echo "  管理功能联调测试：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 全部通过" || echo "⚠️ 存在失败项，请见上方 ❌"
echo "[完成] 测试租户 tenant_id=$TID, 文档 document_id=$DOC_ID"
exit $((FAIL>0?1:0))
