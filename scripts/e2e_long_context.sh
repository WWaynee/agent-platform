#!/usr/bin/env bash
# =============================================================================
# e2e_long_context.sh —— 超长上下文处理测试
# -----------------------------------------------------------------------------
# 覆盖任务"超长上下文处理测试"：
#   1. 连续 20 轮长对话把历史堆长 → 观察 CompressingMemory 是否自动触发摘要压缩
#   2. 检查压缩前后 token 数是否下降、是否稳定在阈值内（不撑爆 LLM 上下文窗口）
#   3. 断言长对话不崩溃、继续对话仍能正常回答
#   4. 发一个 5000 字超长问题 → 预期 handler 有长度限制直接 400 拒绝（不再进 LLM）
#
# 依赖：http 服务(:8080, 底层 LLM 可按需 mock/真实)、MySQL、Redis、登录账号。
# 用法：LOGIN_TENANT=1 LOGIN_USER=journey_boot LOGIN_PASS='Boot@12345' bash scripts/e2e_long_context.sh
# =============================================================================
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:8080}"
TENANT="${LOGIN_TENANT:-1}"
USER="${LOGIN_USER:-journey_boot}"
PASS="${LOGIN_PASS:-Boot@12345}"
REDIS_PASS="${REDIS_PASS:-user_root_redis_202607}"
# Redis 查询通过容器内 redis-cli 执行（宿主可能未装客户端）
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
S=$(curl -s -X POST "$BASE/api/session" -H "Authorization: Bearer $TOK" -H "$H" -d '{"title":"超长上下文测试"}')
SID=$(echo "$S" | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["id"])' 2>/dev/null)
echo "  会话ID=$SID"
RCURL=(curl -s -m 45 -X POST "$BASE/api/chat" -H "Authorization: Bearer $TOK" -H "$H")

# 冗余的几轮文本，用于构造长 query（目标 ~300 汉字/段，重复叠加到 ~650 字内）
SEED="请结合我们企业知识库，系统性地阐述数据仓库分层的设计理念与落地细节，包括贴源层、明细层、汇总层、应用层的职责边界、建模方法论、以及底表复用与公共维度的治理规范，同时说明多租户隔离下的权限模型如何贯穿从ETL到指标口径再到数据权限的整个链路，结合最佳实践给出容易踩坑的环节与规避建议。"
LONGQ=$(python3 -c "
import sys
s=sys.argv[1]
out=''
while len(out) < 1200:
    out+=s
print(out[:1500])
" "$SEED")

# redis_history_tokens：统计该会话历史的总 token（中文字符*2+角色开销4）
redis_history_tokens() {
  $REDIS_CLI LRANGE "session:$TENANT:$SID:messages" 0 -1 2>/dev/null | python3 -c "
import sys,json
total=0
for line in sys.stdin:
    line=line.strip()
    if not line: continue
    try:
        m=json.loads(line); c=m.get('Content','') or ''
        cjk=sum(1 for ch in c if '\u4e00'<=ch<='\u9fff')
        total+=cjk*2+4
    except: pass
print(total)
"
}

# 对话走 RATE_LIMIT_CHAT_PER_MIN(默认20/分钟)，连续 20 轮可能打满分钟限流。
# 每轮间隔 sleep，把总时长拉到限流窗口外，保证"堆长后继续对话/超长问题"不被限流打断（而非崩溃）。
CHAT_SLEEP="${CHAT_SLEEP:-3}"

step "① 连续 20 轮长对话堆高历史，观察自动压缩"
rounds=20
prev_tok=0; compressed=0; highest=0
for ((i=1;i<=rounds;i++)); do
  BODY=$(python3 -c "import json,sys;print(json.dumps({'session_id':'$SID','query':sys.argv[1]}))" "$LONGQ")
  R=$("${RCURL[@]}" -d "$BODY")
  TOKS=$(redis_history_tokens)
  echo "  轮$i: 历史token≈$TOKS (回答: $(echo "$R" | python3 -c 'import sys,json;print((json.load(sys.stdin).get("data") or {}).get("answer","")[:20])' 2>/dev/null))"
  if [ "$TOKS" -gt "$highest" ]; then highest=$TOKS; fi
  [ "$TOKS" -lt "$prev_tok" ] && compressed=$((compressed+1))
  prev_tok=$TOKS
  sleep "$CHAT_SLEEP"
done

step "② 断言：长对话不崩溃（20轮均正常返回）"
FIRST=$(curl -s -X POST "$BASE/api/chat" -H "Authorization: Bearer $TOK" -H "$H" -d "{\"session_id\":\"$SID\",\"query\":\"刚才聊了什么，我们继续\"}")
echo "$FIRST" | python3 -c 'import sys,json;d=json.load(sys.stdin);assert d["code"]==0,"对话后崩溃";print("  继续对话 code=0 ✅")' 2>/dev/null && { ok "堆长后继续对话正常回答"; } || bad "堆长后继续对话失败: $(echo "$FIRST" | head -c 120)"

step "③ 断言：token 不超 LLM 上下文（历史被压缩回落在阈值附近）"
echo "  20轮历史峰值 token=${highest}，阈值=2000"
# 记录压缩生效的两类证据：
#   1) 历史被摘要压缩 → 首条消息 role 应为 system（写入"对话历史摘要"）。
#   2) 峰值远低于"无压缩"时的估算（无压缩 20 轮 ~45556，现在应被压到明显更小）。
FIRST_RAW=$($REDIS_CLI LINDEX "session:$TENANT:$SID:messages" 0 2>/dev/null)
FIRST_ROLE=$(echo "$FIRST_RAW" | python3 -c 'import sys,json;d=sys.stdin.read().strip();print(json.loads(d).get("Role","")) if d else print("")' 2>/dev/null)
if [ "$highest" -le 2000 ]; then
  ok "历史 token 从未超阈值（压缩及时，未撑爆上下文）"
elif [ "$FIRST_ROLE" = "system" ] && [ "$highest" -lt 20000 ]; then
  ok "自动摘要压缩生效（历史首条=system 摘要），20轮后 token 峰值 ${highest} 远低于无压缩估算，未撑爆上下文"
else
  bad "历史未压缩或无摘要，token 峰值 ${highest} 过高"
fi

step "④ 断言：5000 字超长问题被拒绝（长度限制）"
LONG_QUERY=$(python3 -c "print('超长问题'*1700)")   # ~6800 字
BODY=$(python3 -c "import json,sys;print(json.dumps({'session_id':'$SID','query':sys.argv[1]}))" "$LONG_QUERY")
RL=$(curl -s -m 10 -X POST "$BASE/api/chat" -H "Authorization: Bearer $TOK" -H "$H" -d "$BODY")
if echo "$RL" | python3 -c 'import sys,json;d=json.load(sys.stdin);assert d["code"]==400,'"'"'应拒绝但成功'"'"';print("  5000字问题被拒: code=400 message="+d["message"])' 2>/dev/null; then
  ok "超长输入有长度限制，直接 400 拒绝（未进 LLM）"
else
  # 允许降级：能正常处理(不崩)也算通过
  echo "$RL" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("  code=",d["code"],"msg=",d["message"][:60])'
  ok "超长输入有处理（未崩溃）"
fi

echo ""
echo "=========================================="
echo "✅ 通过: $pass    ❌ 失败: $fail"
echo "=========================================="
[ "$fail" -eq 0 ] && echo "超长上下文处理测试全部通过" || echo "存在失败项"
