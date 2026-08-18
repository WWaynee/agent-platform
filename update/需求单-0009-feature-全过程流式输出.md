# 需求单 0009（feature）：对话全过程流式输出（思考中 / 工具调用 / 逐字回答）

- 类型：✨ **feature**（功能需求；0001-0004、0007-0008 为 feature，0005-0006 为 bugfix）
- 状态：📋 **已立项，待实现**（2026-08-19 立项；方案、改动点、风险已评估，**尚未实施**）
- 优先级：🟡 中（体验增强：当前对话为"全部算完一次性返回"，改造成"过程实时反馈 + 回答逐字输出"）
- 模块：`llmclient`（SSE 流式解析）、`agent/engine`（过程事件回调 + Run 改造）、`api/handler/chat.go`（SSE 输出）、`web/js/chat.js`（fetch 流式渲染）、`api/service`（冷轨落库时机）
- 创建日期：2026-08-19

---

## 一、需求背景 / 现状

### 1.1 当前行为（一次性返回）

当前 `POST /api/chat` 是**全算完再一次返回 JSON**：

```
用户发问 → 后端引擎多次调 LLM（决策→检索→再决策→回答）→ 全部完成 → 一次性返回 {answer, tool_calls}
前端 → 等整个响应回来 → 一次性渲染最终回答
```

期间用户**没有任何中间反馈**，只能看到"思考中…"占位（需求单 0006 加的按会话 pending），无法看到：
- 当前处于"思考 / 检索 / 回答"的哪个阶段；
- 工具调用的实时状态；
- 回答逐字刷出的过程。

### 1.2 现状代码依据（改动点所在）

| 层 | 现状 |
|---|---|
| `llmclient/client.go` | `Chat()`（:193）内部 `doPost`（:816）用 `io.ReadAll(resp.Body)`（:850）**一次性读完整响应**；`types.go` 的 `Stream bool` 仅预留字段，**无 SSE 解析**，`stream:true` 时行为未定义 |
| `agent/engine/engine.go` | `LLMClient` 接口（:27）`Chat(ctx, req) (string, error)` 返回最终字符串；`Run(ctx, query) (*AgentResponse, error)`（:137）**同步一次性返回**；主循环内多次 LLM 调用的中间结果只在内部 `msgs` 累积，不外发 |
| `api/handler/chat.go` | 末尾 `response.Success(c, gin.H{answer, session_id, tool_calls})` **一次性返回 JSON**；无 `text/event-stream` / `EventSource` |
| `web/js/chat.js` | `sendMessage` 里 `await api.post('/chat')` **等完整响应**才渲染 `resp.answer`；无 `fetch` 流式读取 |

## 二、要实现的功能点

1. **每一步 LLM 调用都流式**：引擎每次调 LLM（决策 / 检索后整合 / 最终回答）产生的输出增量都推给前端。
2. **思考过程可推送**：展示"正在思考…"（阶段状态事件）。
3. **工具调用状态事件**：推"正在调用知识库检索工具…"（工具名 + 状态）。
4. **最终回答逐字输出**：回答按 token/字符增量逐字刷出（打字机效果）。

> 前置交互前提（结合需求单 0006 的按会话状态机）：流式增量必须**写入对应会话的内存态**，切走/切回仍需正确（不可因流式破坏多会话切换）。

## 三、技术路线评估

**可行，但属结构性改动**，跨后端三层 + 前端一套新机制，不是小改动。核心工作量集中在：

- **后端把"同步函数"改造成"逐步输出"**：`engine.Run` 目前是"全部算完 return"；要流式，需要让引擎**边执行边向外部写出进度**（阶段事件 + token 增量）。这是最大改动点。
- **LLM 客户端增加真实 SSE 流式**：当前只 `io.ReadAll` 一次读完，`stream:true` 未实现。需要新增流式读取 + 分片解析。
- **前后端协议从"一次性 JSON"改为"SSE 事件流"**。
- **冷轨落库/审计时机**与流式融合。

## 四、实现方案（分阶段，推荐先做"阶段事件"，再做"逐字流式"）

### 4.1 整体架构：SSE（Server-Sent Events）事件协议

`POST /api/chat` 在请求带 `?stream=1`（或前端可选）时，响应头改为 `Content-Type: text/event-stream`，并把 ReAct 过程逐步 `flush` 为事件：

```
event: thinking      data: {"msg":"正在思考…"}                       # ReAct 进入某轮、要调 LLM
event: tool_call     data: {"tool":"knowledge_retrieve"}              # 要调用工具
event: tool_result   data: {"tool":"...","preview":"..."}             # 工具返回（含折叠数据）
event: answer_token  data: {"delta":"字"}                             # 最终回答逐字增量
event: done          data: {"session_id":"5","tool_calls":[...]}      # 结束（含完整 answer 供冷轨/审计）
```

- 保活注释行 `: keep-alive\n\n` 定期发送，避免代理/浏览器断开。
- 复用现有 `session_id` 处理（空则自动建会话）。

### 4.2 后端改造点

**【A】`llmclient`：增加真实流式对话（改动大）**

- 新增 `ChatStream(ctx, req, onToken func(string) error) error`（或基于回调）：
  - 请求体带 `"stream": true`；
  - 用 HTTP 客户端逐步读取响应体，按 SSE 分行解析 `data: {...}`，提取 `choices[0].delta.content` 增量；
  - 遇 `data: [DONE]` 结束；传播流式中的超时/断流错误；
  - 与现有 `Chat()` 并行共存（保留非流式返回，供工具内 / 测试使用）。
- 现有 `doPost` 一次性读完整响应不变；新增独立流式读路径。

**【B】`agent/engine`：加"过程事件回调"，改造 `Run`（改动最大）**

- 新增 `ProgressFunc` 回调 / `ProgressEvent`（`type ProgressEvent struct { Phase; Tool; Delta }`）：
  - `thinking`：每轮要调 LLM 前回调；
  - `tool_call` / `tool_result`：调工具前/后回调（对应目前 `Run` 主循环里的工具调用点）；
  - `answer_token`：最终回答逐字回调。
- `ReActEngine` 增加字段 `Progress ProgressFunc`（nil 则跳过，向后兼容）。
- **最终回答：** 目前最终回答是在 `final_answer` 分支 `return &AgentResponse{Answer: parsed.Input}` 一次性给出。改为：解析出最终回答后**逐字回调** `answer_token`（或用 SSE writer 直接写），同时仍在结尾返回完整 `answer` 供冷轨/审计落库。
- 需保持 `Run` 现有调用（`handler.Chat`、测试、`flow`）可用：**建议 Run 签名不变，走 Progress 回调**，避免大范围破坏。

**【C】`api/handler/chat.go`：SSE 输出**

- 请求含 `stream=1` 时，把 `Progress` 回调接到一个 SSE writer，逐步 `c.Stream` / `c.SSEvent` / 手写 flush；
- 结束时发 `done` 事件（含 `session_id` + 完整 `answer` + `tool_calls`）；
- 非流式路径保留（`response.Success`），前端不请求流式时行为不变。
- 冷轨/审计：结尾的 `done` 事件里带完整 `answer`，由原有 `persist` 逻辑落库（时机在 `Run` 内已有保证）。

### 4.3 前端改造点（改动中-大）

- `sendMessage` 从 `await api.post(...)` 改为 `fetch('/api/chat', {method:'POST', body, headers})` + `res.body.getReader()` 读取 SSE 流：
  - 按事件类型分派：
    - `thinking` → 显示/更新"正在思考…"状态条；
    - `tool_call` → 显示"正在调用工具 X…"（复用现有 tool_call 条）；`tool_result` → 用需求单 0008 的折叠展示；
    - `answer_token` → 把 delta **逐字追加**到当前会话消息气泡（打字机效果）；
    - `done` → 结束，写完整 answer 到会话内存态、刷新列表。
  - **与需求单 0006 状态机兼容**：流式增量写入 `targetSt.history` 中正在生成的 answer 消息；
  - 切走会话时：后续事件只更新对应会话状态、不操作当前 DOM（切回时 `renderCurrent` 显示已完成/进行中状态）。
  - 401/业务码处理沿用 `api.js`；SSE 读取需按行缓冲，处理 `event:`/`data:`。
- **渲染的 DOM 事件绑定**：流式逐字更新用 `textContent`（安全），拼接字符串即可。

## 五、涉及文件清单（预期）

| 文件 | 改动类型 |
|------|---------|
| `llmclient/client.go` | **新增** `ChatStream`（SSE 流式读取、`[DONE]` 终止、增量回调）+ 与现有 `Chat` 共存 |
| `llmclient/types.go` | 新增流式事件/增量类型（如 `ChatStreamEvent`） |
| `agent/engine/engine.go` | 新增 `ProgressFunc` 回调 + `ReActEngine.Progress` 字段；`Run` 主循环在思考/工具/回答处回调；最终回答逐字回调 |
| `agent/engine/*.go` | 事件类型定义；保持 `Run` 签名兼容 |
| `api/handler/chat.go` | `stream=1` 时用 SSE writer 逐步 flush 事件；`done` 事件带完整 answer/工具调用 |
| `api/router.go` | 无改动（复用到 `POST /api/chat`）或仅在需要时加参数 |
| `web/js/chat.js` | `sendMessage` 改 fetch 流式，SSE 事件分派；与按会话状态机集成；thinking/tool/answer_token 增量渲染 |
| `api/service/*`（冷轨/审计） | 核对流式落库时机（done 事件后统一落库） |
| 测试 | `llmclient` 流式解析单测、`engine` 事件回调单测、`handler` SSE 单测、前端 mock |

## 六、改动的关键难点 / 风险

1. **后端"同步函数→逐步输出"是架构级**：`Run` 从"全算完 return"变成"边算边回调"，改动波及主循环、工具调用点、最终回答分支；需保证非流式调用（现有测试 / 无 stream 请求）不回归。
2. **流式 + 多会话状态机（需求单 0006）**：逐字增量必须进入"发起会话"的 answer 消息；切走不回写当前 DOM，切回正确显示进行中/已完成。这是前端最容易出 bug 的地方。
3. **`DeepSeek` 等厂商 stream 行为**：SSE 分片格式（`data:` 行、`[DONE]`）、超时、重试、熔断在流式下的语义需兼容（现有容错是否仍适用流式需验证）。
4. **冷轨/审计落库时机**：回答是流式逐字拼出来的，最终完整回答需在结束时落 `chat_messages`（确保历史完整、不丢）。
5. **超时**：流式连接可能持续较久，需合理设置 `keep-alive` 与代理读超时（Nginx `proxy_read_timeout` 已放宽 300s，OK）。

## 七、实施建议（推荐节奏）

**分两阶段落地**，先拿到确定收益、降低风险：

- **阶段 1（推荐先做）："阶段事件流"（thinking / tool_call）**
  - 只推"正在思考…"、"正在调用工具 X…"这些**阶段状态**，不推 token。
  - 后端只需给 `Run` 加"阶段事件回调"，LLM 仍可走现有非流式 `Chat`（一次性），改动面小得多；
  - 前端加"阶段状态条"。体验提升大、风险低。
- **阶段 2（再做）："逐字流式"**
  - 在阶段 1 基础上接真实 `ChatStream`（SSE token 增量）+ 前端打字机渲染。
  - 此步改动最大（LLM 流式解析 + engine 逐字回调 + 前端逐字 + 冷轨），但体验最好。

> 若要**一步到位**，则按第六节一次性实施阶段 1+2，工期长、回归面大。

## 八、范围外（本轮明确不涉及）

- 流式过程中的**取消/中断**（用户中途停止生成）——可作为后续增强；
- **多模态 / 图片流式**；
- 改变现有非流式接口行为（`POST /api/chat` 不加 `stream=1` 时仍返回原 JSON）。

## 九、待与需求方确认

1. **落地节奏**：推荐"先阶段事件流（thinking/tool）→ 再逐字"；是否接受两步走，还是要一步到位？
2. **触发方式**：前端总是请求流式（默认 stream=1），还是保留非流式开关？
3. 回答的"逐字"粒度：按字符（中文友好）还是按 token（可能更细碎）？——倾向按字符（避免 token 切碎中文）。
