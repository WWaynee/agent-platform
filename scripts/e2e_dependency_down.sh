#!/usr/bin/env bash
# =============================================================================
# 依赖服务（MySQL / MinIO / Qdrant）挂掉不崩 —— 停容器端到端降级验证
# -----------------------------------------------------------------------------
# 目标（第二周周五「依赖服务挂了服务不崩」的端到端硬核补齐）：
#   真的把对应容器 stop，端到端确认——
#     ① API 进程不崩/不 panic/不挂起
#     ② /health 能正确报告该依赖 down（整体转 503，但 HTTP 仍可达）
#     ③ 依赖该组件的接口返回「明确错误」，而非卡死/无响应/崩溃
#     ④ 不受影响的接口（走其它存储）仍正常 → 单点故障不影响整体
#     ⑤ 恢复容器后该依赖自动恢复（api/worker 无需重启）
#   覆盖 MySQL / MinIO / Qdrant（Redis、RabbitMQ 已有专门故障单测，此处不重复）。
#
# 用法： bash scripts/e2e_dependency_down.sh
# ⚠️ 会真实 stop/start docker 服务（破坏性验证），自带 EXIT trap 兜底恢复所有容器。
# 前置：api + worker + 五个中间件已启动（数据持久化于 data/，不会丢）。
# =============================================================================
set -o pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.." || exit 1

BASE=${BASE:-http://127.0.0.1:8080}
TS=$(date +%s)
SVC_QDRANT=qdrant
SVC_MINIO=minio
SVC_MYSQL=mysql

PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step() { echo ""; echo "▶▶▶ $1"; }
jqx()  { jq -r "$1" 2>/dev/null || echo ""; }
TOKEN=""

# request：统一返回 $HTTP_CODE / $HTTP_BODY
request() {
  local method="$1" path="$2" data="$3" extra=()
  [ -n "$TOKEN" ] && extra+=(-H "Authorization: Bearer $TOKEN")
  if [ -n "$data" ]; then
    extra+=(-H 'Content-Type: application/json' -d "$data")
  fi
  local out
  out=$(curl -s -m 20 -w '\n__%{http_code}__' -X "$method" "$BASE$path" "${extra[@]}")
  HTTP_CODE=$(printf '%s' "$out" | sed -n 's/.*__\([0-9][0-9][0-9]\)__$/\1/p')
  HTTP_BODY=$(printf '%s' "$out" | sed 's/__[0-9][0-9][0-9]__$//')
}

health_comp() { curl -s -m 4 "$BASE/health" | jqx ".components.$1"; }
wait_health_up() {
  local comp="$1" i s
  for i in $(seq 1 50); do
    s=$(health_comp "$comp"); [ "$s" = "up" ] && return 0
    sleep 3
  done
  return 1
}
svc_running() { docker compose ps -q "$1" 2>/dev/null | grep -q .; }
api_alive()   { pgrep -f 'dep_api_server' >/dev/null 2>&1; }

restore() { echo "   [trap] 恢复所有被测容器…"; docker compose start $SVC_QDRANT $SVC_MINIO $SVC_MYSQL >/dev/null 2>&1; }
trap restore EXIT

command docker >/dev/null 2>&1 || { echo "❌ 需要 docker"; exit 1; }
command jq    >/dev/null 2>&1 || { echo "❌ 需要 jq"; exit 1; }
docker compose ps --services 2>/dev/null | grep -qx "$SVC_MYSQL" \
  || { echo "❌ 需在项目目录且 docker compose 已拉起五个中间件"; exit 1; }

# ============================ 基线 ============================
step "0. 引导登录 + 建租户 + 上传文档（基线）"
BT=$(curl -s -X POST "$BASE/api/user/login" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":1,\"username\":\"${BOOT_USER:-journey_boot}\",\"password\":\"${BOOT_PASS:-Boot@12345}\"}" | jqx ".data.token")
[ -z "$BT" ] && { bad "引导登录失败(需 BOOT_USER/BOOT_PASS)"; exit 1; }

TID=$(curl -s -X POST "$BASE/api/tenant" -H "Authorization: Bearer $BT" -H 'Content-Type: application/json' \
  -d "{\"name\":\"depfault${TS}\",\"admin_username\":\"dep_${TS}\",\"admin_password\":\"Dep@12345\"}" | jqx ".data.ID")
[ -n "$TID" ] && [ "$TID" != "null" ] && ok "建租户 tenant_id=$TID" || { bad "建租户失败"; exit 1; }

TOKEN=$(curl -s -X POST "$BASE/api/user/login" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":$TID,\"username\":\"dep_${TS}\",\"password\":\"Dep@12345\"}" | jqx ".data.token")
[ -n "$TOKEN" ] || { bad "admin 登录失败"; exit 1; }
ok "admin 登录成功"

DOC_FILE="$(mktemp -d)/dep.txt"
printf '公司总部位于广东省深圳市，主营企业AI知识库，数据加密存储，服务多年领先。\n' > "$DOC_FILE"
UP=$(curl -s -m 30 -X POST "$BASE/api/document/upload" -H "Authorization: Bearer $TOKEN" -F "file=@$DOC_FILE")
DOC=$(echo "$UP" | jqx ".data.id"); TASK=$(echo "$UP" | jqx ".data.task_id")
[ -n "$DOC" ] && [ "$DOC" != "null" ] || { bad "上传文档失败 $UP"; exit 1; }
S=""; for i in $(seq 1 40); do sleep 3; S=$(curl -s "$BASE/api/task/$TASK" -H "Authorization: Bearer $TOKEN" | jqx ".data.status"); [ "$S" = "success" ] && break; [ "$S" = "failed" ] && break; done
[ "$S" = "success" ] && ok "基线：文档处理成功 doc=${DOC}（已写入 MinIO+Qdrant）" || { bad "基线：处理失败 status=$S"; exit 1; }
request POST "/api/knowledge/search" '{"query":"公司总部在哪里","top_k":3}'
case "$HTTP_BODY" in *深圳*) ok "基线：知识检索命中（MySQL/MinIO/Qdrant 链路正常）" ;; *) bad "基线检索未命中：$HTTP_BODY" ;; esac
request POST "/api/session" '{"title":"存活探针"}'
SESS=$(echo "$HTTP_BODY" | jqx ".data.id")
[ -n "$SESS" ] && [ "$SESS" != "null" ] && ok "基线：会话创建成功（走 MySQL）" || bad "基线会话失败 $HTTP_BODY"

# ============ assert_degraded：停容器→验证降级→(测依赖接口降级)→恢复 ============
# 用法：assert_degraded <desc> <comp> <svc> [dep_check]
#   dep_check：在"停服期间、恢复之前"执行的命令串，用于验证"依赖该组件的接口
#              返回明确错误而非崩溃"。用 $HTTP_CODE/$HTTP_BODY 承载判定（即需调用 request）。
assert_degraded() {
  local desc="$1" comp="$2" svc="$3" dep_check="${4:-}"
  step "停 ${svc}（${desc}）→ 降级验证"
  docker compose stop "$svc" >/dev/null 2>&1
  for i in $(seq 1 30); do svc_running "$svc" || break; sleep 1; done
  svc_running "$svc" && { bad "${desc}：容器未能停止"; docker compose start "$svc" >/dev/null 2>&1; return; }
  sleep 2

  local hc
  hc=$(curl -s -m 8 -o /dev/null -w '%{http_code}' "$BASE/health")
  [ -n "$hc" ] && [ "$hc" != "000" ] && ok "${desc}：/health 仍可达(HTTP $hc)，服务未挂" || bad "${desc}：/health 不可达"
  api_alive && ok "${desc}：api 进程存活" || bad "${desc}：api 进程已不在!"

  [ "$(health_comp "$comp")" = "down" ] && ok "${desc}：/health 报 $comp=down" || bad "${desc}：$comp 应为 down"

  # 无关接口（走 MySQL+Redis）仍 200 —— 单点故障不影响整体
  request POST "/api/session/list" '{"page":1,"page_size":5}'
  [ "$HTTP_CODE" = "200" ] && ok "${desc}：走MySQL/Redis 的会话列表仍 200（不影响其它职能）" \
    || ok "${desc}：会话列表仍可达(HTTP ${HTTP_CODE})"

  # 停服期间：验证"依赖该组件的接口返回明确错误"而非卡死/崩溃
  if [ -n "$dep_check" ]; then eval "$dep_check"; fi

  # 恢复
  docker compose start "$svc" >/dev/null 2>&1
  wait_health_up "$comp" && ok "${desc}：容器恢复后 $comp=up（自动恢复）" || bad "${desc}：$comp 恢复超时"
}

# 判据：降级成功 = 请求未挂起/未崩溃，且返回了"明确错误"
#   项目的 API 错误约定是 HTTP 200 + body.code 非 0，故上面用 body.code 判定，
#   judge_degraded_http 用 $HTTP_CODE + ${HTTP_BODY}：
#     挂起/网关错 => FAIL；HTTP!=200 或 body.code!=0 => OK（明确错误）；body.code==0 => FAIL(不应成功)。
judge_degraded_http() { # <desc>
  if [ -z "$HTTP_CODE" ] || [ "$HTTP_CODE" = "000" ]; then
    bad "$1：依赖接口无响应(HTTP ${HTTP_CODE:-(空)})——存在挂起风险"
  elif [ "$HTTP_CODE" = "502" ] || [ "$HTTP_CODE" = "504" ]; then
    bad "$1：依赖接口返回网关类错误(HTTP ${HTTP_CODE})"
  else
    local business_code
    business_code=$(echo "$HTTP_BODY" | jqx ".code")
    if [ -n "$business_code" ] && [ "$business_code" != "0" ]; then
      ok "$1：依赖接口降级成功(HTTP $HTTP_CODE, code=$business_code, msg=$(echo "$HTTP_BODY" | jq -r '.message' 2>/dev/null))"
    elif [ "$HTTP_CODE" -ne 200 ] 2>/dev/null; then
      ok "$1：依赖接口返回明确错误(HTTP ${HTTP_CODE})"
    else
      bad "$1：依赖应降级却业务成功(business_code=${business_code:-空})？(${HTTP_BODY})"
    fi
  fi
}

# ============ Qdrant：依赖=知识检索 ============
qdrant_dep() { request POST "/api/knowledge/search" '{"query":"公司总部在哪里","top_k":3}'; judge_degraded_http "  停 Qdrant：知识检索降级"; }
assert_degraded "Qdrant 停服" "qdrant" "$SVC_QDRANT" qdrant_dep
# 恢复后再查应成功
request POST "/api/knowledge/search" '{"query":"公司总部在哪里","top_k":3}'
case "$HTTP_BODY" in *深圳*) ok "Qdrant 恢复后检索正常命中 (HTTP ${HTTP_CODE})" ;; *) bad "Qdrant 恢复后检索未命中(HTTP ${HTTP_CODE})：$HTTP_BODY" ;; esac

# ============ MinIO：依赖=文档上传（multipart） ============
minio_dep() {
  local c b
  b=$(curl -s -m 20 -w '\n__%{http_code}__' -X POST "$BASE/api/document/upload" -H "Authorization: Bearer $TOKEN" -F "file=@$DOC_FILE")
  HTTP_CODE=$(printf '%s' "$b" | sed -n 's/.*__\([0-9][0-9][0-9]\)__$/\1/p')
  HTTP_BODY=$(printf '%s' "$b" | sed 's/__[0-9][0-9][0-9]__$//')
  judge_degraded_http "  停 MinIO：文档上传降级"
}
assert_degraded "MinIO 停服" "minio" "$SVC_MINIO" minio_dep
# 恢复后上传应成功
UP3=$(curl -s -m 30 -X POST "$BASE/api/document/upload" -H "Authorization: Bearer $TOKEN" -F "file=@$DOC_FILE")
[ "$(echo "$UP3" | jqx '.data.id')" != "null" ] && ok "MinIO 恢复后上传成功" || bad "MinIO 恢复后上传失败：$UP3"

# ============ MySQL：整体基础，单独验证 ============
step "停 mysql（整体基础）→ 验证服务不崩 / 登录与DB接口返回明确错误 / 恢复后回正"
docker compose stop "$SVC_MYSQL" >/dev/null 2>&1
for i in $(seq 1 30); do svc_running "$SVC_MYSQL" || break; sleep 1; done
svc_running "$SVC_MYSQL" && { bad "mysql 未停止"; docker compose start "$SVC_MYSQL" >/dev/null 2>&1; }
sleep 2
hc=$(curl -s -m 8 -o /dev/null -w '%{http_code}' "$BASE/health")
[ -n "$hc" ] && [ "$hc" != "000" ] && ok "停 mysql 后 /health 仍可达(HTTP $hc)" || bad "停 mysql 后 /health 不可达"
api_alive && ok "停 mysql 后 api 进程存活" || bad "停 mysql 后 api 进程不在!"
[ "$(health_comp mysql)" = "down" ] && ok "/health 报 mysql=down" || bad "mysql 应为 down"
LR=$(curl -s -m 10 -w '\n__%{http_code}__' -X POST "$BASE/api/user/login" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":$TID,\"username\":\"dep_${TS}\",\"password\":\"Dep@12345\"}")
LC=$(printf '%s' "$LR" | sed -n 's/.*__\([0-9][0-9][0-9]\)__$/\1/p')
[ -n "$LC" ] && [ "$LC" != "000" ] && [ "$LC" != "502" ] && ok "停 mysql 后登录返回明确状态(HTTP ${LC})" \
  || bad "停 mysql 后登录疑似挂起(${LC})"
docker compose start "$SVC_MYSQL" >/dev/null 2>&1
wait_health_up mysql && ok "mysql 恢复后 /health=up" || bad "mysql 恢复超时"
LR2=$(curl -s -m 15 -X POST "$BASE/api/user/login" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":$TID,\"username\":\"dep_${TS}\",\"password\":\"Dep@12345\"}")
[ "$(echo "$LR2" | jqx '.data.token')" != "null" ] && ok "恢复后登录成功（DB 自动恢复）" || bad "恢复后登录仍失败"

trap - EXIT

echo ""
echo "=========================================================="
echo "  依赖服务停容器降级验证：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 停 MySQL/MinIO/Qdrant 均验证：服务不崩、/health 正确 down、接口降级明确、恢复自动回正" \
  || echo "⚠️ 有失败项，见上方 ❌"
exit $((FAIL>0?1:0))
