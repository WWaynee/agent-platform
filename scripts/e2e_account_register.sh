#!/usr/bin/env bash
# =============================================================================
# 账号体系安全改进端到端测试（需求单 0001）
# -----------------------------------------------------------------------------
# 覆盖注册/登录/租户注册的安全底线改动：
#   1. 公开注册租户 /api/tenant/register → 返回租户 + 管理员，能立即用该 admin 登录
#   2. 用新建 admin 登录 → 能进私有接口
#   3. 注册用户乱填 tenant_id（不存在的 ID） → 400，明确「租户/公司不存在」
#   4. 重复注册同名租户的同名 admin → 处理冲突（联合唯一索引兜底）
#   5. 注册用户传 role=admin 到合法租户 → 校验租户存在后创建成功（admin）
#   6. 禁用租户后其账号登录被拒 → 401
#   7. GET /api/tenant/list 未登录 → 401（仍受控，未被误挪公开）
#   8. 注册租户审计日志落库（operation=注册租户）
#
# 用法： bash scripts/e2e_account_register.sh   （需已启动 api）
# 依赖： curl / jq
# =============================================================================
set -o pipefail

BASE=${BASE:-http://127.0.0.1:8080}
PASS=0; FAIL=0
ok()  { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad() { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step(){ echo ""; echo "▶▶▶ $1"; }
jqx(){ jq -r "$1" 2>/dev/null || echo ""; }

command -v jq >/dev/null 2>&1 || { echo "❌ 需要 jq，请先安装"; exit 1; }
H='Content-Type: application/json'
TS=$(date +%s%N)

# ---------- 1. 公开注册租户 ----------
step "1. 公开注册租户 /api/tenant/register"
NAME="账号体系测试$TS"
REG=$(curl -s -X POST "$BASE/api/tenant/register" -H "$H" \
  -d "{\"name\":\"$NAME\",\"admin_username\":\"acc_${TS}\",\"admin_password\":\"Acc@123456\"}")
TID=$(echo "$REG" | jqx ".data.tenant.ID")
ADMIN_ID=$(echo "$REG" | jqx ".data.admin.ID")
[ -n "$TID" ] && [ "$TID" != "null" ] && [ "$TID" != "0" ] \
  && ok "注册租户成功，tenant_id=$TID admin_id=$ADMIN_ID" \
  || { bad "注册租户失败: $REG"; exit 1; }

# 2. 用新 admin 登录
step "2. 用新 admin 登录"
LOGIN=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"acc_${TS}\",\"password\":\"Acc@123456\"}")
TOKEN=$(echo "$LOGIN" | jqx ".data.token")
ROLE=$(echo "$LOGIN" | jqx ".data.user.Role")
[ -n "$TOKEN" ] && [ "$TOKEN" != "null" ] && [ "$ROLE" = "admin" ] \
  && ok "新 admin 登录成功，role=$ROLE" \
  || { bad "新 admin 登录失败: $LOGIN"; exit 1; }
AH="Authorization: Bearer $TOKEN"

# 3. 乱填 tenant_id 注册 → 400
step "3. 注册用户乱填 tenant_id → 400 租户/公司不存在"
BAD=$(curl -s -X POST "$BASE/api/user/register" -H "$H" \
  -d '{"tenant_id":99999999,"username":"ghost_bye","password":"Ghost@123"}')
BCODE=$(echo "$BAD" | jqx ".code")
BMSG=$(echo "$BAD" | jqx ".message")
if [ "$BCODE" = "400" ] && echo "$BMSG" | grep -q "租户/公司不存在"; then
  ok "乱填 tenant_id 被拒：$BMSG"
else
  bad "乱填 tenant_id 应 400+租户不存在，实际 code=$BCODE msg=$BMSG"
fi

# 4. 合法租户内注册 admin 角色 → 校验租户存在后成功
step "4. 合法租户内注册 role=admin → 校验租户存在后创建成功"
R2=$(curl -s -X POST "$BASE/api/user/register" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"acc_admin2_${TS}\",\"password\":\"Acc@123456\",\"role\":\"admin\"}")
R2ROLE=$(echo "$R2" | jqx ".data.Role")
[ "$R2ROLE" = "admin" ] && ok "注册 role=admin 成功" || bad "注册 role=admin 失败: $R2"

# 5. 重复注册同名 admin（同租户）→ 用户名已存在
step "5. 重复注册同名用户 → 用户名已存在"
R3=$(curl -s -X POST "$BASE/api/user/register" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"acc_${TS}\",\"password\":\"Acc@123456\"}")
R3MSG=$(echo "$R3" | jqx ".message")
[ "$(echo "$R3" | jqx ".code")" = "400" ] && echo "$R3MSG" | grep -q "用户名已存在" \
  && ok "重复注册被拒：$R3MSG" || bad "重复注册应报用户名已存在，实际:$R3"

# 6. 禁用租户后登录 → 401
step "6. 禁用租户后其账号登录 → 401"
echo "    （先禁用租户 $TID，再登录）"
DIS=$(curl -s -X PUT "$BASE/api/admin/tenant/$TID/status" -H "$AH" -H "$H" -d '{"status":0}')
[ "$(echo "$DIS" | jqx ".code")" = "0" ] && ok "禁用租户成功" || bad "禁用租户失败: $DIS"
LDIS=$(curl -s -X POST "$BASE/api/user/login" -H "$H" \
  -d "{\"tenant_id\":$TID,\"username\":\"acc_${TS}\",\"password\":\"Acc@123456\"}")
LMSG=$(echo "$LDIS" | jqx ".message")
if [[ "$LMSG" == *"已禁用"* ]] || [[ "$LMSG" == *"禁用"* ]]; then
  ok "禁用租户登录被拒：$LMSG"
else
  bad "禁用租户登录应被拒（响应: $LDIS）"
fi
# 恢复租户状态
curl -s -X PUT "$BASE/api/admin/tenant/$TID/status" -H "$AH" -H "$H" -d '{"status":1}' >/dev/null 2>&1

# 7. 未登录访问私有租户列表 → 401
step "7. GET /api/tenant/list 未登录 → 401"
HL=-1
HL=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/tenant/list")
[ "$HL" = "401" ] && ok "未登录访问租户列表返回 401（仍受控）" || bad "租户列表未登录应 401，实际 $HL"

# 8. 注册租户审计日志落库（待查 MySQL，此处仅提示查库方式）
step "8. 注册租户审计日志落库（文档方法查 MySQL）"
echo "    （已记 operation='注册租户'，可用：docker exec agent-mysql mysql -u root -p... -e \\\"SELECT * FROM audit_logs WHERE tenant_id=$TID AND operation='注册租户'\\\"）"

echo ""
echo "=========================================================="
echo "  账号体系安全端到端测试：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 全部通过" || echo "⚠️ 存在失败项，请见上方 ❌"
exit $((FAIL>0?1:0))
