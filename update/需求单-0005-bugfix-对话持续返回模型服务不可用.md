# 需求单 0005（bugfix）：对话持续返回「抱歉，模型服务暂时不可用，请稍后再试。」

- 类型：🐞 **bugfix**（缺陷修复；0001-0004 均为 feature，本文档为首个 bugfix 需求单）
- 状态：✅ **已修复并验证**（2026-08-19 定位根因 + 修复 + 回归单测；已提交推送 `origin/main`）
- 优先级：🔴 **P0**（核心对话功能完全不可用：提问 2 次后无论问什么都拿不到回答；且对话历史无法持久化、刷新后全空）
- 模块：`agent/interfaces/interfaces.go`（`WithRuntimeContext`）、`agent/engine/engine.go`（`persistFullHistory` / ReAct 循环）、`agent/interfaces/context_test.go`（回归单测）、`web/js/chat.js`（前端渲染）
- 创建日期：2026-08-19
- 完成日期：2026-08-19

---

## 一、问题表现（用户实际观察到的现象）

1. **对话 1~2 轮后稳定失效**：正常提问（触发知识库检索工具调用）1~2 次之后，**后续无论问什么**，前端对话区都只返回「抱歉，模型服务暂时不可用，请稍后再试。」——不是偶发，是可稳定复现。
2. **历史丢失 / 清空**：继续问新问题，之前对话历史消失；点击会话列表其它会话，对话区不变化；**刷新页面后点任意会话都是空的，没有历史**。
3. （关联前端表现，上一轮已修）发送问题后过一会没有返回结果，整个对话区内容被清空。

## 二、根因分析（依据实际代码与后端日志）

### 2.1 后端日志证据（`/tmp/agent_api_8081.log`）

```
"msg":"LLM 调用失败" "error":"请求中断: context deadline exceeded"   ← 稳定出现，且集中在工具调用之后
"msg":"写完整历史失败" ... "error":"context canceled"                  ← 冷轨 MySQL 历史写入全部失败
```

按 `trace_id` 串联一次对话：`LLM 成功 → 工具(knowledge_retrieve)执行 → 之后的 LLM 调用全部 context deadline exceeded`。失败时 `prompt_tokens=0`、latency 很短——**不是 DeepSeek 慢等 30s 超时，而是请求在发出前就被判定 deadline 过期**。

### 2.2 根因：`WithRuntimeContext` 链式污染 AgentContext 的运行时 ctx（rctx）

`agent/engine/engine.go` ReAct 主循环中，工具执行前会构造一个带超时的 ctx：

```go
toolCtx, cancel := context.WithTimeout(ctx.ToContext(nil), e.ToolTimeout) // ToolTimeout 默认 10s（defaultToolTimeout）
defer cancel()
out, terr := e.ToolManager.ExecuteTool(*(ctx.WithRuntimeContext(toolCtx)), parsed.Action, parsed.Input)
```

**原实现 `WithRuntimeContext` 是"修改原对象并返回同一实例"**（`agent/interfaces/interfaces.go`）：

```go
func (c *AgentContext) WithRuntimeContext(ctx context.Context) *AgentContext {
	c.rctx = ctx // 直接写回原对象
	return c     // 返回同一个指针
}
```

由此产生**一次会话级的状态污染**：

1. 第 1 轮调 LLM 成功（此时 `rctx == nil`，`ToContext(nil)` 走 `context.Background()`）。
2. LLM 决策调工具 → `ctx.WithRuntimeContext(toolCtx)` 把**带 10s deadline 的 toolCtx** 永久写进了这个会话的 `ctx.rctx`。
3. 工具执行完 `defer cancel()` → 该 toolCtx **立即被取消**。
4. 之后同一会话内：
   - **再调 LLM**：`llmCtx := ctx.ToContext(nil)` → `rctx != nil` 直接返回**那个已被 cancel 的 toolCtx**；`llmclient.doPost` 检测到外部 ctx 已有 deadline（`hasDeadline=true`，不再用 30s 整体预算）且已取消 → **立即 `context deadline exceeded`** → 引擎兜底返回「抱歉，模型服务暂时不可用」。
   - **冷轨异步写库**：`persistFullHistory` 内 `base := ctx.ToContext(nil)` 拿到同一个已取消的 ctx → `storage.AppendChatMessage` 全部报 `context canceled` → **历史一条都写不进去**。

**这同时解释了全部现象**：为什么是"2 次之后稳定失效"（第 1 次对话通常不需要工具 → rctx 未被污染；一旦触发工具，rctx 即被永久污染）；为什么历史全空（冷轨写入全失败）；为什么刷新后无历史（`GET /api/session/:id/messages` 读的是冷轨 MySQL，而冷轨是空的）。

### 2.3 关联前端 bug（`web/js/chat.js`，同批修复）

原 `sendMessage` 在发送后**立即 `await loadMessages()` 读冷轨历史刷新**界面；而冷轨写入是异步 goroutine（且当时全部 `context canceled` 失败），读到空历史后 `renderMessages([])` 执行 `box.innerHTML=''` → **整个对话区被清空**（用户看到"没有返回结果、内容被清空"）。

## 三、解决方案

### 3.1 `WithRuntimeContext` 改为返回副本（根因修复，`agent/interfaces/interfaces.go`）

```go
// WithRuntimeContext 返回一个携带运行时 ctx 的 AgentContext 副本，不修改原对象。
// ⚠️ 修复 P0：原来链式写回原对象会把工具超时(10s)的 toolCtx 永久污染进会话 ctx.rctx，
// 工具执行完 cancel 后，后续 LLM 调用/冷轨异步写库拿到已取消的 ctx → 全部 context deadline exceeded / context canceled。
func (c *AgentContext) WithRuntimeContext(ctx context.Context) *AgentContext {
	cp := *c // 值拷贝
	cp.rctx = ctx
	return &cp
}
```

- 工具执行时 `ExecuteTool(*(ctx.WithRuntimeContext(toolCtx)), ...)` 拿到的是**副本**，工具内 `ToContext(nil)` 仍能用上工具超时 deadline；但**原 `ctx.rctx` 保持干净**，后续 LLM 调用、`persist` 不再被工具超时污染。
- 全仓仅 `agent/engine/engine.go` 一处调用，语义从"污染原对象"改为"返回携带工具 ctx 的副本"，无其它调用方受影响。

### 3.2 `persistFullHistory` 使用独立的持久化 ctx（双保险，`agent/engine/engine.go`）

冷轨写入属于"尽力而为"的异步持久化，**不应继承调用链的 deadline/cancel**。改为基于 `context.Background()` 组装，仅带 tenant/user/trace_id：

```go
base := interfaces.WithTenantUser(context.Background(), ctx.TenantID, ctx.UserID)
if tid := ctx.TraceID(); tid != "" {
	base = interfaces.WithTraceID(base, tid)
}
```

### 3.3 前端：发送后不再读异步冷轨刷新界面（`web/js/chat.js`）

- `sendMessage` 发送后直接用 `POST /api/chat` 响应里**实时可靠**的 `resp.answer` / `resp.tool_calls` 渲染，不再 `await loadMessages()` 覆盖界面。
- `renderMessages` 增加防御：冷轨历史瞬时为空且界面已有内容时**不清空**。
- `loadMessages` 仅保留用于"切换会话 / 手动刷新"这类冷轨通常已写完的场景。

## 四、涉及文件清单

| 文件 | 改动类型 |
|------|---------|
| `agent/interfaces/interfaces.go` | **修复**：`WithRuntimeContext` 改为返回副本，不再污染原对象 rctx（P0 根因）|
| `agent/engine/engine.go` | **修复**：`persistFullHistory` 用独立 `context.Background()` 组装，不继承被取消的 ctx |
| `agent/interfaces/context_test.go` | **新增回归单测**：`TestWithRuntimeContext_DoesNotPolluteOriginal`（验证原对象 rctx 不被污染）|
| `web/js/chat.js` | **修复**：发送后实时渲染 `resp.answer`，不读异步冷轨清空界面；空历史不清空防御 |
| `update/需求单-0005-bugfix-对话持续返回模型服务不可用.md` | **新增**（本文档）|

## 五、验证记录

- [x] 根因定位：后端日志 `trace_id` 串联（LLM 成功→工具→后续全部 `context deadline exceeded`）+ `写完整历史失败 ... context canceled`。
- [x] 回归单测 `TestWithRuntimeContext_DoesNotPolluteOriginal`：工具超时 ctx 只作用于返回副本，原对象 rctx 不变（PASS）。
- [x] `go vet ./...` / `go test ./...` / `go build ./...` 全绿（agent/api 包回归通过）。
- [x] 前端 `node --check web/js/chat.js` 通过。
- [x] 修复后已**重新编译并重启** 8081 试用后端（`bin/api_new` 时间戳更新），`/health` healthy、前端 3000 反带链路通。

## 六、提交记录

- `7a036a3` fix(前端对话): 发送后不再读异步冷轨历史清空界面，改用 chat 响应实时渲染；renderMessages 空历史不清空防御
- `dfc5c07` fix(引擎): `WithRuntimeContext` 改返回副本避免污染原 ctx 的 rctx（工具超时 deadline 被带进后续 LLM/冷轨异步写而超时/丢失）；`persistFullHistory` 改用独立 Background ctx；补回归单测

## 七、范围外 / 遗留

- DeepSeek 本身偶发响应慢导致的 `context deadline exceeded`（latency 4-9s 抖动）属外部因素，修复后不会再有"必然超时"，但偶发慢仍可能触发兜底回答（属正常容错，可后续调大 `LLM_TIMEOUT_SECONDS` 缓解）。
- 历史数据：修复前因冷轨写入失败产生的空历史会话无法自动补回（数据从未落库）；修复后**新产生的**对话历史正常持久化。
