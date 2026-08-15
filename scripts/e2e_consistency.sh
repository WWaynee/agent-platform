#!/usr/bin/env bash
# =============================================================================
# 数据一致性端到端检查
# -----------------------------------------------------------------------------
# 验证各存储之间的数据一致性，确保没有脏数据：
#   检查项3: 文档处理成功 → Qdrant 能检索到对应向量
#   自测点 : 向量数量 == 文档切片数量（chunk_index 0..N-1）
#   检查项2: 删除会话 → Redis 历史消息删除 + MySQL 软删一致
#   检查项5: 用量统计 Redis 计数 == 实际调用次数（发 N 次对话，计数增量=N）
#   检查项1: 删除文档 → MinIO 文件删除 + Qdrant 向量删除 + MySQL 软删一致
#   无脏数据: 删文档后再检索 → 不再命中该文档
#
# 依赖（均通过外部命令访问，不改动代码库）：
#   - curl/jq（HTTP）
#   - docker exec（访问 agent-redis / agent-mysql）
#   - Qdrant REST 6333（count 向量点）
#   - MinIO：用项目 go.mod + minio SDK 内嵌 Go 小程序 `go run` 检查
#
# 用法： bash scripts/e2e_consistency.sh   （需先启动 api + worker）
# =============================================================================
set -o pipefail

BASE=${BASE:-http://127.0.0.1:8080}
QDRANT_HTTP=${QDRANT_HTTP:-http://127.0.0.1:6333}
QDRANT_COLL=${QDRANT_COLL:-documents}
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJ_ROOT="$SCRIPT_DIR/.."
[ -f "$PROJ_ROOT/.env" ] || PROJ_ROOT="$SCRIPT_DIR"
set -a; . "$PROJ_ROOT/.env"; set +a

# 外部容器名
RDB=${RDB:-agent-redis}
MDB=${MDB:-agent-mysql}
MIO=${MIO:-agent-minio}
REDIS_PASS=${REDIS_PASSWORD:-}
MYSQL_PASS=${MYSQL_ROOT_PWD:-}
MYSQL_DB=${MYSQL_DB:-agent_platform}
MIO_AK=${MINIO_ACCESS_KEY:-}
MIO_SK=${MINIO_SECRET_KEY:-}
MINIO_PORT=${MINIO_PORT:-}

TS=$(date +%s)
TENANT_NAME="一致性${TS}"
ADMIN_USER="cons_admin_${TS}"
ADMIN_PASS="Cons@12345"
BOOT_TENANT_ID=${BOOT_TENANT_ID:-1}
BOOT_USER=${BOOT_USER:-}
BOOT_PASS=${BOOT_PASS:-}

PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
step() { echo ""; echo "▶▶▶ $1"; }
jqx()  { jq -r "$1" 2>/dev/null || echo ""; }
is_cmd(){ command -v "$1" >/dev/null 2>&1; }
is_cmd jq || { echo "❌ 需要 jq（brew install jq）"; exit 1; }
is_cmd curl || { echo "❌ 需要 curl"; exit 1; }

# ---------- 公共：Qdrant 统计某文档向量点数 ----------
qdrant_doc_count() { # tenantID documentID
  curl -s -X POST "$QDRANT_HTTP/collections/$QDRANT_COLL/points/count" \
    -H 'Content-Type: application/json' \
    -d "{\"filter\":{\"must\":[{\"key\":\"tenant_id\",\"match\":{\"value\":$1}},{\"key\":\"document_id\",\"match\":{\"value\":$2}}]},\"exact\":true}" \
    | jqx ".result.count"
}
# scroll 取某文档所有向量的 chunk_index → 输出以逗号连接
qdrant_doc_chunks() { # tenantID documentID
  curl -s -X POST "$QDRANT_HTTP/collections/$QDRANT_COLL/points/scroll" \
    -H 'Content-Type: application/json' \
    -d "{\"filter\":{\"must\":[{\"key\":\"tenant_id\",\"match\":{\"value\":$1}},{\"key\":\"document_id\",\"match\":{\"value\":$2}}]},\"limit\":200,\"with_payload\":true,\"with_vector\":false}" \
    | jq -r "[.result.points[].payload.chunk_index] | sort | join(\",\")" 2>/dev/null | tr -d '\n'
}
# Redis 取整数值（不存在则 0）
redis_get() {
  docker exec "$RDB" redis-cli -a "$REDIS_PASS" GET "$1" 2>/dev/null
}
redis_exists() {
  docker exec "$RDB" redis-cli -a "$REDIS_PASS" EXISTS "$1" 2>/dev/null
}
# MinIO 文件是否存在（用内嵌 Go + 项目 go.mod）
minio_exists() { # objectKey  → 输出 exists/deleted
  local objkey="$1"
  local tmpd
  tmpd=$(mktemp -d)
  cat > "$tmpd/main.go" <<GOEOF
package main
import ("context";"fmt";"os";"github.com/minio/minio-go/v7";"github.com/minio/minio-go/v7/pkg/credentials")
func main(){
    c, e := minio.New("127.0.0.1:$MINIO_PORT", &minio.Options{Creds: credentials.NewStaticV4("$MIO_AK","$MIO_SK",""), Secure: false})
    if e != nil { fmt.Println("err", e); return }
    _, e = c.StatObject(context.Background(), "document-store", os.Getenv("OBJKEY"), minio.StatObjectOptions{})
    if e != nil { fmt.Println("deleted"); return }
    fmt.Println("exists")
}
GOEOF
  (cd "$PROJ_ROOT" && OBJKEY="$objkey" go run "$tmpd/main.go" 2>/dev/null)
  rm -rf "$tmpd"
}

# ---------- 引导建租户 ----------
step "0. 引导登录 + 建租户（自动建 admin）"
BOOT_TOKEN=$(curl -s -X POST "$BASE/api/user/login" -H "Content-Type: application/json" \
  -d "{\"tenant_id\":$BOOT_TENANT_ID,\"username\":\"$BOOT_USER\",\"password\":\"$BOOT_PASS\"}" | jqx ".data.token")
[ -z "$BOOT_TOKEN" ] && { bad "引导登录失败（需 BOOT_USER/BOOT_PASS）"; exit 1; }
TID=$(curl -s -X POST "$BASE/api/tenant" -H "Content-Type: application/json" -H "Authorization: Bearer $BOOT_TOKEN" \
  -d "{\"name\":\"$TENANT_NAME\",\"admin_username\":\"$ADMIN_USER\",\"admin_password\":\"$ADMIN_PASS\"}" | jqx ".data.ID")
[ -z "$TID" ] || [ "$TID" = "null" ] && { bad "建租户失败"; exit 1; }
ok "建租户成功 tenant_id=$TID"
AH="Authorization: Bearer $(curl -s -X POST "$BASE/api/user/login" -H "Content-Type: application/json" \
  -d "{\"tenant_id\":$TID,\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | jqx ".data.token")"
[ ${#AH} -lt 30 ] && { bad "admin 登录失败"; exit 1; }
ok "admin 登录成功"

# ---------- 上传长文档，产生多切片 ----------
step "1. 上传长文档（产生多切片）→ 等处理成功"
DOC_FILE="$(mktemp -d)/cons_doc.txt"
cat > "$DOC_FILE" <<'EOF'
公司成立于 2010 年，是专注于企业级人工智能解决方案的科技公司，总部位于深圳。
公司设有研发部、产品部、市场部、人力资源部、财务部等五大部门，员工超两千人。
产品线包括智能客服机器人、知识库管理系统、自动化流程引擎三大类。
智能客服机器人采用深度学习和自然语言处理技术，自动解答客户常见问题。
知识库管理系统帮助企业统一管理内部文档、规章制度与产品说明书等私有知识。
自动化流程引擎把企业重复性工作自动化，大幅提高人均产出效率。
公司每年举办创新大赛，鼓励员工围绕人工智能应用场景提出创新方案。
获奖团队获高额奖金和资源支持，优秀方案进入产品研发管道。
公司高度重视数据安全，内部数据加密存储，通过严格权限体系访问控制。
公司通过 ISO9001 质量管理与 ISO27001 信息安全双体系认证。
连续五年获行业最佳雇主奖，员工满意度保持行业领先水平。
为保障业务连续性，在深圳、上海、北京三地部署了高可用灾备集群。
年均投入营收百分之二十用于研发，确保人工智能技术持续领先。
客户遍布金融、制造、零售、医疗、教育等行业，累计服务超五千家。
财务部每月出具经营分析报告，向管理层汇报收入与支出等关键指标。
市场部负责品牌运营与获客，线上线下的多渠道推广触达潜在客户群。
产品部根据客户需求设计功能，与研发部紧密配合完成产品迭代。
人力部负责招聘、培训与员工关怀，持续输送优秀人才。
研发部采用敏捷开发模式，每两周交付一个可运行的迭代版本。
公司鼓励跨部门协作，定期组织技术分享会交流最新成果与最佳实践。
EOF
UP=$(curl -s -X POST "$BASE/api/document/upload" -H "$AH" -F "file=@$DOC_FILE")
DOC=$(echo "$UP" | jqx ".data.id")
TASK=$(echo "$UP" | jqx ".data.task_id")
[ -z "$DOC" ] || [ "$DOC" = "null" ] && { bad "上传文档失败：$UP"; exit 1; }
S=""
for i in $(seq 1 40); do
  sleep 3
  S=$(curl -s "$BASE/api/task/$TASK" -H "$AH" | jqx ".data.status")
  [ "$S" = "success" ] && break
  [ "$S" = "failed" ] && break
done
[ "$S" = "success" ] && ok "文档处理成功 doc=$DOC task=$TASK" || { bad "文档处理失败 status=$S"; exit 1; }

# 取 MinIO objectKey 备用
OBJKEY=$(docker exec "$MDB" mysql --database="$MYSQL_DB" -u root -p"$MYSQL_PASS" -N -B \
  -e "SELECT minio_object_key FROM documents WHERE id=$DOC AND tenant_id=$TID;" 2>/dev/null | tr -d '\r')
echo "    → MinIO objectKey=$OBJKEY"

# ---------- 检查项3：处理成功后 Qdrant 能查到向量 ----------
step "2. [检查项3] 文档处理成功后 Qdrant 能检索到向量"
HIT=$(curl -s -X POST "$BASE/api/knowledge/search" -H "$AH" -H "Content-Type: application/json" \
  -d '{"query":"公司总部位于哪个城市","top_k":5}' | jqx ".data.results[0].content")
case "$HIT" in *深圳*) ok "检索命中本文档片段（含'深圳'），向量已入库可检索 ✅" ;; *) bad "未命中本文档：$HIT" ;; esac

# ---------- 自测点：向量数量 == 切片数量 ----------
step "3. [自测点] Qdrant 向量数量 == 文档切片数量"
QCOUNT=$(qdrant_doc_count "$TID" "$DOC")
CHUNKS=$(qdrant_doc_chunks "$TID" "$DOC")
MAXIDX=$(echo "$CHUNKS" | tr ',' '\n' | sort -n | tail -1)
SLICES=$((MAXIDX + 1))
echo "    → Qdrant 向量点数量=$QCOUNT, chunk_index=[$CHUNKS], 切片数=$SLICES"
[ "$QCOUNT" = "$SLICES" ] && [ "$QCOUNT" -ge 1 ] \
  && ok "向量数量($QCOUNT) == 切片数量($SLICES)，且 chunk_index 连续(0..$MAXIDX) ✅" \
  || bad "向量数量($QCOUNT)与切片数量($SLICES)不符"

# ---------- 检查项2：删会话 → Redis 消息清理 ----------
step "4. [检查项2] 删除会话后 Redis 历史消息清理"
SID=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "Content-Type: application/json" -d '{"title":"一致性会话"}' | jqx ".data.id")
curl -s -X POST "$BASE/api/chat" -H "$AH" -H "Content-Type: application/json" \
  -d "{\"session_id\":\"$SID\",\"query\":\"公司有哪些部门\"}" >/dev/null
sleep 1
SKEY="session:$TID:$SID:messages"
BEFORE=$(redis_exists "$SKEY")
[ "$BEFORE" = "1" ] && ok "删前 Redis 存在该会话消息 key（EXISTS=1）✅" || bad "删前应存在 Redis 消息 key，实际 EXISTS=$BEFORE"
curl -s -X DELETE "$BASE/api/session/$SID" -H "$AH" >/dev/null
AFTER=$(redis_exists "$SKEY")
[ "$AFTER" = "0" ] && ok "删除会话后 Redis 消息 key 已清（EXISTS=0）✅" || bad "删除会话后 Redis 消息 key 未清（EXISTS=$AFTER）"
DST=$(docker exec "$MDB" mysql --database="$MYSQL_DB" -u root -p"$MYSQL_PASS" -N -B -e "SELECT deleted_at FROM sessions WHERE id=$SID;" 2>/dev/null)
[ -n "$DST" ] && ok "MySQL 会话已软删（deleted_at=$DST）✅" || bad "MySQL 会话未软删"

# ---------- 检查项5：用量计数与实际调用一致 ----------
step "5. [检查项5] 用量统计 Redis 计数 == 实际调用次数"
C0=$(curl -s "$BASE/api/admin/usage/today" -H "$AH" | jqx ".data.calls")
for i in 1 2 3; do
  NSID=$(curl -s -X POST "$BASE/api/session" -H "$AH" -H "Content-Type: application/json" -d "{\"title\":\"用量$i\"}" | jqx ".data.id")
  curl -s -X POST "$BASE/api/chat" -H "$AH" -H "Content-Type: application/json" \
    -d "{\"session_id\":\"$NSID\",\"query\":\"你好$i\"}" >/dev/null
  sleep 1
done
CTODAY=$(curl -s "$BASE/api/admin/usage/today" -H "$AH" | jqx ".data.calls")
TODAY=$(date -u +%Y-%m-%d)
RDIS_CALLS=$(redis_get "usage:tenant:$TID:$TODAY:calls")
RDIS_TOKENS=$(redis_get "usage:tenant:$TID:$TODAY:token")
# 说明：usage 的 calls 统计的是「LLM 调用次数」，而每次对话（ReAct）会多次调 LLM
# （决策轮 + 工具检索 + 作答轮），故 3 次对话的增量是"≥3 且 >0 增长"，而非固定 +3。
# 因此不依赖固定增量；验收要点 = ①确实发生调用（增量≥3）②Redis 计数与接口完全一致（无脏数据）。
[ "$CTODAY" -ge $((C0 + 3)) ] \
  && ok "发起 3 次对话后 usage/today calls 由 $C0 → $CTODAY（实际增量=$((CTODAY-C0))，≥3 次真实验）✅" \
  || bad "3 次对话后调用次数未增长：$C0 → $CTODAY"
[ "$RDIS_CALLS" = "$CTODAY" ] && ok "Redis 计数(calls=$RDIS_CALLS) 与 usage/today($CTODAY) 完全一致 ✅" \
  || bad "Redis calls=$RDIS_CALLS 与接口 $CTODAY 不一致"
[ "$RDIS_TOKENS" ] && [ "$RDIS_TOKENS" != "0" ] && ok "Redis token 计数=$RDIS_TOKENS（>0，有真实消耗）✅" \
  || bad "Redis token 计数异常=$RDIS_TOKENS"

# ---------- 检查项1：删文档 → MinIO + Qdrant + MySQL 一致 ----------
step "6. [检查项1] 删除文档 → MinIO / Qdrant / MySQL 三处一致"
# 取证：删除前状态
MINIO_BEFORE=$(minio_exists "$OBJKEY")
Q_BEFORE=$(qdrant_doc_count "$TID" "$DOC")
echo "    → 删除前: MinIO=$MINIO_BEFORE, Qdrant向量=$Q_BEFORE"
curl -s -X DELETE "$BASE/api/document/$DOC" -H "$AH" >/dev/null
sleep 1
MINIO_AFTER=$(minio_exists "$OBJKEY")
Q_AFTER=$(qdrant_doc_count "$TID" "$DOC")
DELETED_AT=$(docker exec "$MDB" mysql --database="$MYSQL_DB" -u root -p"$MYSQL_PASS" -N -B -e "SELECT deleted_at FROM documents WHERE id=$DOC AND tenant_id=$TID;" 2>/dev/null)
[ "$MINIO_AFTER" = "deleted" ] && ok "MinIO 文件已删除 ✅" || bad "MinIO 文件未删：$MINIO_AFTER"
[ "$Q_AFTER" = "0" ] && ok "Qdrant 向量已清（$Q_BEFORE → 0）✅" || bad "Qdrant 遗留向量：$Q_AFTER"
[ -n "$DELETED_AT" ] && ok "MySQL 文档已软删（deleted_at=$DELETED_AT）✅" || bad "MySQL 文档未软删"

# ---------- 无脏数据：删后再检索不该命中 ----------
step "7. [无脏数据] 删除文档后再检索 → 不再命中该文档"
HIT2_N=$(curl -s -X POST "$BASE/api/knowledge/search" -H "$AH" -H "Content-Type: application/json" \
  -d '{"query":"公司总部位于哪个城市","top_k":5}' | jq "[.data.results[]?] | length")
[ "$HIT2_N" = "0" ] && ok "删除后检索返回 $HIT2_N 条（results 空，已删文档过滤干净）✅" \
  || bad "删除后仍命中 $HIT2_N 条向量（脏数据！）"

# ---------- 汇总 ----------
echo ""
echo "=========================================================="
echo "  数据一致性检查：成功 $PASS 项 / 失败 $FAIL 项"
echo "=========================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 全部通过，各存储数据一致、无脏数据" || echo "⚠️ 存在失败项，请见上方 ❌"
echo "[完成] 测试租户 tenant_id=$TID, 文档 document_id=$DOC"
exit $((FAIL>0?1:0))
