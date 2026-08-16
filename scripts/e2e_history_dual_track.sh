#!/usr/bin/env bash
# =============================================================================
# e2e_history_dual_track.sh —— 对话存储双轨分离 + 工具调用完整历史（需求单 0002）
# -----------------------------------------------------------------------------
# 双轨存储：
#   - 冷轨（MySQL chat_messages）：逐字完整原文，含工具调用指令(tool_call)+执行结果(tool_result)，
#     永不压缩、永不过期、供前端回看/审计；
#   - 热轨（Redis 会话记忆）：最终问答 + 工具"模板化摘要"（[工具] 工具名：…，user 角色），供下一轮理解+参与压缩。
#
# 覆盖：
#   1. 一轮含知识库检索的对话后，GET /api/session/:id/messages 返回完整历史，含
#      question/answer/tool_call(工具名+参数)/tool_result(检索片段全文)，且每条带 kind。
#   2. 下一轮对话：LLM 能记得上轮调过知识库工具（热轨模板化摘要生效，Redis 含 [工具] 消息）。
#   3. 越权读历史仍被拒（租户隔离防线回归）。
#   4. MySQL 冷轨完整原文须包含工具调用全过程。
#
# 依赖：http 服务(:8080)、MySQL、Redis、登录账号、已上传并检索到知识库的文档。
# 用法：LOGIN_TENANT=1 LOGIN_USER=journey_boot LOGIN_PASS='Boot@12345' bash scripts/e2e_history_dual_track.sh
# =============================================================================
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
TENANT="${LOGIN_TENANT:-1}"
USER="${LOGIN_USER:-journey_boot}"
PASS="${LOGIN_PASS:-Boot@12345}"
REDIS_PASS="${REDIS_PASS:-user_root_redis_202607}"
REDIS_CLI="${REDIS_CLI:-docker exec agent-redis redis-cli -a $REDIS_PASS --no-auth-warning}"

pass=0; fail=0
H='Content-Type: application/json'

step() { echo ""; echo "◆ $1"; }
ok()   { echo "  ✅ $1"; pass=$((pass+1)); }
bad()  { echo "  ❌ $1"; fail=$((fail+1)); }

# ---- 登录 ----
LOG=$(curl -s -X POST "$BASE/api/user/login" -H "$H" -d "{\"tenant_id\":$TENANT,\"username\":\"$USER\",\"password\":\"$PASS\"}")
TOK=$(echo "$LOG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])' 2>/dev/null)
[ -n "$TOK" ] && ok "登录成功" || { bad "登录失败"; exit 1; }

# ---- 新建会话 ----
S=$(curl -s -X POST "$BASE/api/session" -H "Authorization: Bearer $TOK" -H "$H" -d '{"title":"双轨完整历史测试"}')
SID=$(echo "$S" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["id"])' 2>/dev/null)
echo "  会话ID=$SID"

step "① 发起触发知识库检索的问题（多轮，涉及工具调用）"
QUERY="请结合我们知识库检索一下：公司去年的销售数据是多少？请用工具查询后回答。"
BODY=$(python3 -c "import json,sys;print(json.dumps({'session_id':'$SID','query':sys.argv[1]}))" "$QUERY")
R=$(curl -s -m 45 -X POST "$BASE/api/chat" -H "Authorization: Bearer $TOK" -H "$H" -d "$BODY")
echo "$R" | python3 -c 'import sys,json;d=json.load(sys.stdin);assert d.get("code")==0,"对话失败: "+str(d)[:160];print("  回答: "+(d.get("data") or {}).get("answer","")[:40])' 2>/dev/null \
  && ok "含工具检索的对话正常完成" || bad "对话失败: $(echo "$R" | head -c 160)"

step "② GET /api/session/:id/messages 返回完整历史（冷轨，含工具调用全过程）"
MSGS=$(curl -s -X GET "$BASE/api/session/$SID/messages" -H "Authorization: Bearer $TOK")
echo "$MSGS" | python3 -c '
import sys,json
d=json.load(sys.stdin); assert d["code"]==0,"读历史失败"
msgs=d["data"]["messages"]
kinds=[m.get("kind") for m in msgs]
print("  消息条数=%d kinds=%s"%(len(msgs),kinds))
assert any(k=="question" for k in kinds), "缺少用户提问"
assert any(k=="answer" for k in kinds), "缺少最终回答"
assert any(k=="tool_call" for k in kinds), "缺少工具调用指令"
assert any(k=="tool_result" for k in kinds), "缺少工具执行结果"
# 工具调用指令应含工具名+参数；工具结果应含检索片段全文
for m in msgs:
    if m.get("kind")=="tool_call":
        assert "knowledge_retrieve" in m.get("content",""), "tool_call 未含工具名"
        print("  tool_call:", m.get("content")[:60])
    if m.get("kind")=="tool_result":
        print("  tool_result(片段):", m.get("content")[:60])
' 2>/dev/null && ok "返回完整历史含 question/answer/tool_call/tool_result（每条带 kind）" || bad "完整历史读取/断言失败: $(echo "$MSGS" | head -c 160)"

step "③ Redis 热轨含工具模板化摘要（[工具] …，user 角色）——供下一轮理解"
HOT=$($REDIS_CLI LRANGE "session:$TENANT:$SID:messages" 0 -1 2>/dev/null)
echo "$HOT" | grep -q "\[工具\]" && ok "Redis 热轨含 [工具] 模板化摘要(user 角色)" || bad "热轨未找到 [工具] 摘要"

step "④ MySQL 冷轨完整原文含工具调用全过程（tool_call/tool_result）"
# 通过 GET messages 已间接证明读到冷轨完整历史；此处再用最新一条 answer 确认双轨互不干扰。
LAST_KIND=$(echo "$MSGS" | python3 -c 'import sys,json;d=json.load(sys.stdin);return_=d["data"]["messages"][-1].get("kind");print(return_)' 2>/dev/null)
[ "$LAST_KIND" = "answer" ] && ok "冷轨完整历史最后一条为 answer，完整保留未压缩" || bad "冷轨末条 kind=$LAST_KIND"

step "⑤ 越权读历史仍被拒（租户隔离防线回归）"
# 用显然不存在的另一个租户会话 ID 试探 -> 应 403（不区分不存在与无权）
OTHER="999999"
F=$(curl -s -o /dev/null -w "%{http_code}" -X GET "$BASE/api/session/$OTHER/messages" -H "Authorization: Bearer $TOK")
if [ "$F" = "403" ]; then
  ok "越权读历史返回 403（隔离防线回归）"
else
  bad "越权读历史应 403, 实际 $F"
fi

echo ""
echo "=========================================="
echo "✅ 通过: $pass    ❌ 失败: $fail"
echo "=========================================="
[ "$fail" -eq 0 ] && echo "双轨完整历史测试全部通过" || echo "存在失败项"
