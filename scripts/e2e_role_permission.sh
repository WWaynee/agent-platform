#!/usr/bin/env bash
# =============================================================================
# 多角色权限联调端到端测试
# -----------------------------------------------------------------------------
# 验证管理员(admin)与普通成员(member)的权限差异与越权拦截：
#   建租户+admin → admin 注册一个普通成员 → 双方登录
#   → member 上传文档(应成功) → member 调管理接口(应403)
#   → member 查自己对话历史(正常) → member 删 admin 文档(应被拒/无权删除他人文档)
#   → 对照：member 删自己文档(应成功)；member 用 admin 会话(应403)
#   → 对照：admin 调管理接口(应成功)
#
# 用法： bash scripts/e2e_role_permission.sh   （需先启动 api + worker）
# 依赖： curl / jq
# =============================================================================
set -o pipefail

BASE=${BASE:-http://127.0.0.1:8080}
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
for d in "$SCRIPT_DIR" "$SCRIPT_DIR/.."; do [ -f "$d/.env" ] && ENV_FILE="$d/.env" && break; done
[ -n "${ENV_FILE:-}" ] && set -a && . "$ENV_FILE" && set +a

TS=$(date +%s)
TENANT_NAME="多角色${TS}"
ADMIN_USER="role_admin_${TS}"
ADMIN_PASS="Role@12345"
MEMBER_USER="role_member_${TS}"
MEMBER_PASS="Mem@12345"

BOOT_TENANT_ID=${BOOT_TENANT_ID:-1}
BOOT_USER=${BOOT_USER:-}
BOOT_PASS=${BOOT_PASS:-}

PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step() { echo ""; echo "▶▶▶ $1"; }
jqx()  { jq -r "$1" 2>/dev/null || echo ""; }
command -v jq >/dev/null 2>&1 || { echo "❌ 需要 jq（brew install jq）"; exit 1; }

H='Content-Type: application/json'

# ---------- 准备：引导登录 + 建租户（自动生成 admin） ----------
step "0. 引导登录（用现有租户账号，建新租户）"
BOOT_TOKEN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$BOOT_TENANT_ID,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jqx ".data.token")
[ -z "$BOOT_TOKEN" ] && { bad "引导登录失败（需 BOOT_USER/BOOT_PASS）"; exit 1; }
ok "引导登录成功"

step "1. 创建新租户（自动建 admin）"
TENANT=$(curl -s -X POST "$BASE/api/tenant" -H "$H" -H "Authorization: Bearer $BOOT_TOKEN" \
  -d "{\"name\":\"$TENANT_NAME\",\"admin_username\":\"$ADMIN_USER\",\"admin_password\":\"$ADMIN_PASS\"}")
TID=$(echo "$TENANT" | jqx ".data.ID")
[ -z "$TID" ] || [ "$TID" = "null" ] && { bad "创建租户失败：$TENANT"; exit 1; }
ok "创建租户成功 tenant_id=$TID"

# ---------- admin 登录 ----------
step "2. admin 登录"
ATOKEN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | jqx ".data.token")
[ -z "$ATOKEN" ] || [ "$ATOKEN" = "null" ] && { bad "admin 登录失败"; exit 1; }
ok "admin 登录成功（拿到 token）"
AH="Authorization: Bearer $ATOKEN"

# ---------- 步骤：admin 注册一个普通成员 ----------
step "3. admin 注册一个普通成员成员账号"
MREG=$(curl -s -X POST "$BASE/api/user/register" -H "$AH" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"$MEMBER_USER\",\"password\":\"$MEMBER_PASS\",\"role\":\"member\"}")
MROLE=$(echo "$MREG" | jqx ".data.Role")
[ "$MROLE" = "member" ] && ok "admin 注册普通成员成功（role=member）✅" || { bad "注册成员失败：$MREG"; exit 1; }

step "4. 普通成员登录"
MTOKEN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"$MEMBER_USER\",\"password\":\"$MEMBER_PASS\"}" | jqx ".data.token")
[ -z "$MTOKEN" ] || [ "$MTOKEN" = "null" ] && { bad "member 登录失败"; exit 1; }
ok "member 登录成功"
MH="Authorization: Bearer $MTOKEN"

# ---------- 准备文档：admin 上传一篇（供 member 尝试删除） ----------
step "5. [准备] admin 上传文档"
printf '管理员上传的文档，多角色权限验证用。\n' > /tmp/role_${TS}_admin.txt
ADOC=$(curl -s -X POST "$BASE/api/document/upload" -H "$AH" -F "file=@/tmp/role_${TS}_admin.txt" | jqx ".data.id")
ADOC_TASK=$(curl -s "$BASE/api/document/list?page=1&page_size=50" -H "$AH" | jq -r "[.data.list[] | select(.id==$ADOC)][0].id" 2>/dev/null)
[ -z "$ADOC" ] || [ "$ADOC" = "null" ] && { bad "admin 上传文档失败"; exit 1; }
ok "admin 上传文档成功 doc=$ADOC"

# ---------- 步骤：member 上传文档 -> 成功 ----------
step "6. member 上传文档 → 应成功"
printf '普通成员上传的文档，私有内容。\n' > /tmp/role_${TS}_member.txt
MUP=$(curl -s -X POST "$BASE/api/document/upload" -H "$MH" -F "file=@/tmp/role_${TS}_member.txt")
MDOC=$(echo "$MUP" | jqx ".data.id")
[ -z "$MDOC" ] || [ "$MDOC" = "null" ] && bad "member 上传文档失败：$MUP" \
  || ok "member 上传文档成功 doc=$MDOC"

# ---------- 步骤：member 调管理接口 -> 403 ----------
step "7. member 调用管理接口（改工具配置/查用量/查任务列表）→ 应 403"
DENIED=1
for EP_METHOD in "GET /api/admin/tool-config" "PUT /api/admin/tool-config/knowledge_retrieve" "GET /api/admin/usage/today" "GET /api/admin/task/list"; do
  M=${EP_METHOD%% *}; P=${EP_METHOD#* }
  [ "$M" = "GET" ] && R=$(curl -s "$BASE$P" -H "$MH") || R=$(curl -s -X "$M" "$BASE$P" -H "$MH" -H "$H" -d '{"is_enable":false}')
  [ "$(echo "$R" | jqx '.code')" = "403" ] || { DENIED=0; bad "member 访问 $P 未被拒（code=$(echo "$R"|jqx '.code')）"; }
done
[ "$DENIED" -eq 1 ] && ok "member 调 工具配置/用量/任务列表 均被拒（403 仅管理员）✅"

# 确认 member 的改工具请求未生效（工具仍 enable，若生效说明越权成功=严重问题）
PERM=$(curl -s "$BASE/api/admin/tool-config" -H "$AH" | jqx ".data.list[] | select(.tool_name==\"knowledge_retrieve\") | .is_enable")
[ "$PERM" = "true" ] && ok "member 越权改工具未生效（is_enable 仍为 true）✅" || bad "工具被越权改掉了！is_enable=$PERM"

# ---------- 步骤：member 删 admin 文档 -> 应删不掉 ----------
step "8. member 删 admin 上传的文档(doc=$ADOC) → 应被拒（用户级隔离）"
DR=$(curl -s -X DELETE "$BASE/api/document/$ADOC" -H "$MH")
case "$DR" in
  *"无权删除他人文档"*) ok "member 删除 admin 文档被拒（无权删除他人文档）✅" ;;
  *) bad "member 竟删掉了 admin 文档？$DR" ;;
esac
# 确认文档未被删（admin 仍能查到）
ADOC_ALIVE=$(curl -s "$BASE/api/document/$ADOC" -H "$AH" | jqx ".code")
[ "$ADOC_ALIVE" = "0" ] && ok "admin 文档 doc=$ADOC 仍存在未被删 ✅" || bad "admin 文档丢失：code=$ADOC_ALIVE"

# ---------- 对照：member 删自己的文档 -> 成功 ----------
step "9. [对照] member 删自己的文档(doc=$MDOC) → 应成功"
MYDR=$(curl -s -X DELETE "$BASE/api/document/$MDOC" -H "$MH")
[ "$(echo "$MYDR" | jqx '.code')" = "0" ] && ok "member 删除自己的文档成功（用户级隔离按上传者区分）✅" \
  || bad "member 删自己文档应成功但失败：$MYDR"

# ---------- 步骤：member 查自己的对话历史 -> 正常 ----------
step "10. member 查自己的对话历史 → 应正常"
SID=$(curl -s -X POST "$BASE/api/session" -H "$MH" -H "$H" -d '{"title":"成员会话"}' | jqx ".data.id")
curl -s -X POST "$BASE/api/chat" -H "$MH" -H "$H" -d "{\"session_id\":\"$SID\",\"query\":\"你好\"}" >/dev/null
SL=$(curl -s "$BASE/api/session/list?page=1&page_size=20" -H "$MH")
SL_TOTAL=$(echo "$SL" | jqx ".data.total")
IN_LIST=$(echo "$SL" | jq --argjson sid "$SID" "[.data.list[] | select(.ID==\$sid)][0].ID" 2>/dev/null)
[ "$IN_LIST" = "$SID" ] 2>/dev/null && ok "member 查自己对话历史正常（会话 $SID 在列表）✅" \
  || bad "member 会话历史列表异常 total=$SL_TOTAL"

# 越权：member 用别人的会话 -> 应被拒（会话同样有用户级隔离）
ADMIN_SID=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "$H" -d '{"title":"admin会话"}' | jqx ".data.id")
XS=$(curl -s -X POST "$BASE/api/chat" -H "$MH" -H "$H" -d "{\"session_id\":\"$ADMIN_SID\",\"query\":\"越权\"}")
case "$XS" in *"无权访问"*|*"不存在"*) ok "member 用 admin 会话越权被拒（会话用户级隔离）✅" ;; *) bad "member 竟能用 admin 会话？$XS" ;; esac

# ---------- 对照：admin 调管理接口 -> 成功 ----------
step "11. [对照] admin 调管理接口 → 应成功"
ADMIN_OK=1
A1=$(curl -s "$BASE/api/admin/tool-config" -H "$AH");   [ "$(echo "$A1" | jqx '.code')" = "0" ] || { ADMIN_OK=0; bad "admin 查工具配置失败"; }
A2=$(curl -s "$BASE/api/admin/usage/today" -H "$AH");   [ "$(echo "$A2" | jqx '.code')" = "0" ] || { ADMIN_OK=0; bad "admin 查用量失败"; }
A3=$(curl -s "$BASE/api/admin/task/list" -H "$AH");     [ "$(echo "$A3" | jqx '.code')" = "0" ] || { ADMIN_OK=0; bad "admin 查任务列表失败"; }
[ "$ADMIN_OK" -eq 1 ] && ok "admin 可正常调 工具配置/用量/任务列表 管理接口 ✅"

# ---------- 汇总 ----------
echo ""
echo "=========================================================="
echo "  多角色权限联调测试：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 全部通过" || echo "⚠️ 存在失败项，请见上方 ❌"
echo "[完成] 测试租户 tenant_id=$TID"
exit $((FAIL>0?1:0))
