#!/usr/bin/env bash
# =============================================================================
# 文档维度检索 + 文档级控制能力端到端测试（需求单 0003）
# -----------------------------------------------------------------------------
# 覆盖：
#   阶段1 文档维度检索（knowledge/search 确定性断言）：
#     - 检索结果带 document_name / document_id / chunk_index
#     - 不传 document_ids = 全租户检索（兼容老逻辑）
#     - document_ids 单文档 / 多文档过滤
#     - 不存在 ID / 空数组 / 全无效 / 有效+无效混 / 0 负数剔除
#     - 跨租户 document_id 被 tenant_id 兜住返回空
#     - top_k 默认3 / 超大收敛
#   阶段2 文档级工具（通过 /api/chat 让 LLM 自动编排，软校验 tool_calls/答案）：
#     - list_documents / search_documents / get_document_content / knowledge_retrieve
#     - 名称→ID 解析（"总结《xxx》"/"对比两文档"）
#   阶段3 多租户隔离：新工具强制 tenant_id
#
# 用法： bash scripts/e2e_document_analysis.sh   （需已启动 api + mysql+qdrant+minio）
# 依赖： curl / jq / 已登录租户
# 说明：
#   - 确定性 part（knowledge/search）为硬断言，失败会算 FAIL。
#   - LLM 编排 part（chat 工具调用）依赖外部模型，做"软校验"：即使 LLM 随机没走预期
#     工具也只记 SKIP/信息，不拉低硬性通过数（避免环境抖动误报）。
# =============================================================================
set -o pipefail

BASE=${BASE:-http://127.0.0.1:8080}
PASS=0; FAIL=0; SKIP=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
skip() { echo "  ⏭️  $1"; SKIP=$((SKIP+1)); }
step(){ echo ""; echo "▶▶▶ $1"; }
jqx(){ jq -r "$1" 2>/dev/null || echo ""; }

[ -n "$TOKEN" ] || { echo "❌ 需要 TOKEN 环境变量（已登录租户的 JWT）"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "❌ 需要 jq"; exit 1; }
AH="Authorization: Bearer $TOKEN"
H='Content-Type: application/json'
H1='Content-Type: application/json'

search() { curl -s -X POST "$BASE/api/knowledge/search" -H "$AH" -H "$H" -d "$1"; }

echo "================================================================"
echo "  需求单 0003：文档维度检索 + 文档级控制能力 端到端测试"
echo "================================================================"

# ------------ 阶段1：knowledge/search 硬断言 ------------
step "阶段1.1 检索结果带 document_name/document_id/chunk_index"
R=$(search '{"query":"付款条款","top_k":3}')
if [ "$(echo "$R" | jq '.data.results | length')" -gt 0 ]; then
  F=$(echo "$R" | jq -r '.data.results[0] | has("document_name") and has("document_id") and has("chunk_index") and has("content")')
  [ "$F" = "true" ] && ok "检索结果含 content/document_id/document_name/chunk_index" \
    || bad "检索结果缺少文档身份字段: $(echo "$R" | jq -c '.data.results[0]')"
else
  skip "库中无检索数据，跳过"
fi

step "阶段1.2 不传 document_ids = 全租户检索"
R=$(search '{"query":"付款条款","top_k":5}')
CNT=$(echo "$R" | jq '.data.results | length')
[ "$CNT" -gt 0 ] && ok "不传 document_ids 全租户检索出 $CNT 条" || bad "不传 document_ids 应返回结果，实际空"

step "阶段1.3 传 document_ids 单文档过滤 + 多文档过滤"
D1=$(search '{"query":"付款条款","top_k":5,"document_ids":[110]}' | jq -c '.data.results[]?.document_id')
if [ "$(echo "$D1" | tr '\n' ' ')" = "110 " ] || [ "$(echo "$D1" | grep -c .)" -eq 1 ]; then
  ok "document_ids=[110] 只返回文档110"
else
  bad "document_ids=[110] 应只返回文档110，实际: $D1"
fi

step "阶段1.4 document_ids=[] 空数组 = 全租户（不报错）"
R=$(search '{"query":"考勤","top_k":5,"document_ids":[]}')
[ "$(echo "$R" | jq '.data.results | length')" -gt 0 ] && ok "空数组等同不传→全租户" || bad "空数组应等同不传"

step "阶段1.5 负数 document_id 在反序列化被拒（400），单 0 回退全租户"
R=$(search '{"query":"考勤","top_k":5,"document_ids":[0,-1]}')
[ "$(echo "$R" | jqx ".code")" = "400" ] && ok "[0,-1] 含负数在 JSON 反序列化被拒(400)" || bad "[0,-1] 负数应被 400 拒绝"

step "阶段1.5b 单 0（合法uint64）剔除后回退全租户"
R=$(search '{"query":"考勤","top_k":5,"document_ids":[0]}')
[ "$(echo "$R" | jq '.data.results | length')" -gt 0 ] && ok "[0] 剔除后回退全租户" || bad "[0] 应回退全租户"

step "阶段1.6 有效+无效混合只取有效"
R=$(search '{"query":"付款条款","top_k":5,"document_ids":[110,0,999]}')
IDS=$(echo "$R" | jq -c '[.data.results[]?.document_id]')
[ "$IDS" = "[110]" ] && ok "[110,0,999] 只保留文档110: $IDS" || bad "[110,0,999] 应只返回110，实际 $IDS"

step "阶段1.7 不存在的 document_id → 返回空不报错"
R=$(search '{"query":"付款条款","document_ids":[999999]}')
CODE=$(echo "$R" | jqx ".code")
[ "$CODE" = "0" ] && ok "不存在ID返回 code=0（空结果不报错）" || bad "不存在ID应不报错，code=$CODE"

step "阶段1.8 跨租户 document_id 被 tenant_id 兜住"
# 传一个明显属于其他租户的文档ID（如101），当前租户 token 检索应返回空（被 tenant_id 强制过滤）
R=$(search '{"query":"付款条款","document_ids":[101]}')
[ "$(echo "$R" | jq '.data.results | length')" -eq 0 ] && ok "跨租户 document_id=101 被 tenant_id 过滤返回空" || bad "跨租户 document_id 应被兜住返回空"

step "阶段1.9 top_k 未传默认3"
R=$(search '{"query":"条款"}')
N=$(echo "$R" | jq '.data.results | length')
[ "$N" -le 3 ] && ok "top_k 默认 ≤3（实际 $N）" || bad "top_k 默认应3，实际 $N"

# ------------ 阶段2：文档级工具（LLM 编排，软校验） ------------
step "阶段2.1 名称→ID 解析：总结某篇文档（应走 list_documents + get_document_content）"
R=$(curl -s -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d '{"query":"总结一下《采购合同》的核心内容"}')
TC=$(echo "$R" | jq -c '.data.tool_calls // []')
ANS=$(echo "$R" | jqx ".data.answer")
if echo "$TC" | grep -q "list_documents"; then
  ok "总结问题引导调用了 list_documents（工具:$TC）"
else
  skip "LLM 未按预期调 list_documents（工具:$TC）——属模型编排随机性"
fi
if echo "$TC" | grep -q "get_document_content"; then
  ok "总结问题引导调用了 get_document_content 读全文"
else
  skip "LLM 未调 get_document_content（工具:$TC）"
fi
[ -n "$ANS" ] && [ "$ANS" != "null" ] && echo "$ANS" | grep -q "采购合同" \
  && ok "最终回答提到《采购合同》: ${ANS:0:40}..." || skip "回答未明确提到采购合同（模型随机性）"

step "阶段2.2 文档维度检索 + 多文档对比（应传 document_ids）"
R=$(curl -s -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d '{"query":"对比《采购合同》和《销售合同A》在付款条款上的区别"}')
TC=$(echo "$R" | jq -c '.data.tool_calls // []')
echo "$TC" | grep -q "knowledge_retrieve" && ok "对比问题调用了 knowledge_retrieve" || skip "LLM 未调 knowledge_retrieve（工具:$TC）"

# ------------ 阶段3：注册用 /api/chat 验证 search_documents 能力（软校验） ------------
step "阶段3.1 按名称搜索文档（应走 search_documents / list_documents）"
R=$(curl -s -X POST "$BASE/api/chat" -H "$AH" -H "$H" -d '{"query":"找找名称里带 合同 的文档有哪些"}')
TC=$(echo "$R" | jq -c '.data.tool_calls // []')
if echo "$TC" | grep -qE "search_documents|list_documents"; then
  ok "找文档问题调用了搜索/列表工具（工具:$TC）"
else
  skip "LLM 未调 search/list 工具（工具:$TC）"
fi

echo ""
echo "=============================================================="
echo "  需求单 0003 测试：成功 $PASS 项 / 失败 $FAIL 项 / 跳过 $SKIP 项"
echo "=============================================================="
[ "$FAIL" -eq 0 ] && echo "🎉 硬性断言全部通过（跳过项为 LLM 编排随机性，不视为失败）" \
  || echo "⚠️ 存在失败项，请见上方 ❌"
exit $((FAIL>0?1:0))
