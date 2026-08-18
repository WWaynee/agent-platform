# 需求单 0006（bugfix）：会话切换失效——点击会话不切换 / 加载中切换错乱 / 切回不显示上一次回答状态

- 类型：🐞 **bugfix**（缺陷修复；0001-0004 为 feature，0005-0006 为 bugfix）
- 状态：✅ **已修复并验证**（2026-08-19 定位根因 + 状态机重写修复；Node 场景复现通过 + 用户实测通过；已提交推送 `origin/main`）
- 优先级：🔴 **P0**（核心对话交互不可用：无法正常切换会话查看各自历史，多会话场景混乱）
- 模块：`web/js/chat.js`（前端会话/对话逻辑）
- 创建日期：2026-08-19
- 完成日期：2026-08-19

---

## 一、问题表现（用户实际观察到的现象）

1. **点击会话列表切换对话，对话窗口内容不切换**：点 A、点 B，中间对话区始终显示同一份内容，不会跟随左侧选中项变化。
2. **在对话 A 中提问、回答还在加载时，点击左侧会话 B，对话窗口内容不切换**；仍停在 A 或显示错乱内容。
3. **在对话 A 的回答还在加载时，切到 B 再切回 A**，应该显示 A 上一次回答是"已完成"还是"还在加载中"，但实际状态不对（看不到清晰的加载中/完成反馈）。

**正常预期**：点击哪个会话，中间对话区就切换到哪个会话的内容；在加载中切换也应立即切走；切回一个"正在加载中"的会话时，应保留并显示其"思考中…"状态，回答返回后更新为答案。

## 二、根因分析（依据实际代码）

### 2.1 原实现是"单一全局渲染"，无按会话的状态隔离

修复前 `web/js/chat.js` 采用**单一全局消息流**方案：

- 只有一个全局 `currentSessionId` + 一个 `messageBox`（DOM 节点），没有为每个会话保存**独立的渲染状态（history/pending）**。
- 会话切换依赖 `loadMessages` **读取冷轨异步历史**（`GET /api/session/:id/messages`，数据源为 MySQL `chat_messages`）后 `renderMessages` 整体重建 `messageBox.innerHTML`。
- 发送消息是异步 `POST /api/chat`，回答返回后直接操作**当前** `messageBox`。

由此产生的三类缺陷：

1. **冷轨历史是异步后台写入**（需求单 0002 的 `persistFullHistory` 用 `go func()` 异步落库）。切换会话的瞬间，目标会话的冷轨可能**尚未写完 / 读空 / 读到旧内容** → 界面渲染空或旧内容 → 表现为"点击会话不切换 / 显示了错的内容"。
2. **异步回答写回"当前 DOM"而非"发起会话"**：在 A 提问、回答还在返回时切到 B，A 的回答返回后直接操作 `messageBox`（此时已是 B 的界面）→ B 的界面被 A 的内容污染 / 或 A 的内容被覆盖 → "加载中切换错乱"。
3. **无 per-会话的 pending/完成判定**：切回某个加载中的会话时，不知道它当时是"正在加载"还是"已完成"，无法正确展示"思考中… / 回答"。

### 2.2 修复方案缺陷（并发竞态 / 无状态隔离）

旧版通过"发送后 reload 冷轨历史"刷新、以及单一 messageBox 直接追加，多个会话的异步回调互相竞争同一个 DOM，缺少"结果归属会话 + 仅当是当前会话才渲染"的原子约束。

## 三、解决方案（`web/js/chat.js` 状态机重写）

把会话/对话逻辑重构为**按会话维护独立内存状态**，异步结果按会话归位：

### 3.1 按会话的内存态

```js
let sessions = {};              // key = sessionId(string)，值 = { history: [], pending: bool }
let currentSessionId = null;    // 当前选中会话
function sessionState(id) { if (!sessions[id]) sessions[id] = { history: [], pending: false }; return sessions[id]; }
```

- **每个会话独立**保存其消息数组 `history` 与"是否加载中" `pending`。

### 3.2 切换会话立即渲染该会话内容（不再依赖冷轨）

```js
async function selectSession(id) {
  currentSessionId = id;
  高亮左栏;
  renderCurrent();             // 立即用该会话内存态渲染（含 thinking/完成）
  if (!st.loaded && !st.pending) { st.loaded = true; loadMessagesInto(id); } // 冷轨仅后台补齐细节，不阻塞
}
function renderCurrent() {
  渲染 currentSessionId 的内存 history;
  if (st.pending) box.appendChild(buildThinking());  // 加载中显示"思考中…"
}
```

- 切换时**立即**从该会话内存态渲染，冷轨历史只是后台静默补齐（工具调用细节），不再阻塞即时切换。

### 3.3 异步回答写回"发起会话"，仅当是当前会话才渲染 DOM

```js
async function sendMessage() {
  const atSession = currentSessionId;       // 记录发起会话
  pushToSession(atSession, {kind:'question', ...});  // 回显用户消息
  st.pending = true;                         // 标记该会话加载中
  ...
  const resp = await api.post('/chat', {...});
  const targetId = atSession ? atSession : newSessionId; // 结果写回发起会话
  const targetSt = sessionState(targetId);
  targetSt.pending = false;
  targetSt.history.push({kind:'answer', content: resp.answer});
  if (String(targetId) === String(currentSessionId)) renderCurrent(); // 仅当仍是当前会话才渲染
}
function pushToSession(sid, m) {
  st.history.push(m);
  if (String(sid) === String(currentSessionId)) { /* 追加渲染到当前 DOM */ }
}
```

- **回答结果恒定写回发起会话**的状态，不因用户中途切走而丢失或写入别的会话 DOM；
- 仅当该会话**仍是当前查看会话**时才操作 DOM；否则只更新状态，用户切回时 `renderCurrent` 自动展示。

### 3.4 "思考中"占位按会话管理

- 每个会话的 `pending` 独立；`renderCurrent` 依据它追加/移除 `thinking-bubble`。
- 切回加载中的会话仍显示"思考中…"，回答返回 `pending=false`，`renderCurrent` 更新为回答。

## 四、涉及文件清单

| 文件 | 改动类型 |
|------|---------|
| `web/js/chat.js` | **重写**：按会话内存态（`sessions`）、`selectSession`/`renderCurrent`/`pushToSession`/`sessionState` 替代原单一 `renderMessages`/`loadMessages` 直接渲染；异步结果按会话归位 |

## 五、验证记录

- [x] Node mock DOM 隔离复现三场景：
  - 场景1：点 A 显示 A 内容、点 B 显示 B 内容 ✅
  - 场景2：A 加载中点 B，正确切到 B 内容 ✅
  - 场景3：A 加载中点 B 再点回 A，显示"思考中…"；回答返回后显示完整回答 ✅
- [x] `node --check web/js/chat.js` 通过。
- [x] **用户实测通过**（2026-08-19）：三个现象在浏览器强刷修复文件后均不再复现。
- [x] 需**浏览器强刷**（`Cmd/Ctrl+Shift+R` 或 Disable cache）以加载新版 chat.js（前端为静态文件，浏览器可能缓存旧版——这也是"以为没修好"的常见原因）。

## 六、提交记录

- `cbae67d` fix(前端对话): 修复会话切换——按会话维护内存态(history/pending)，切换立即渲染该会话内容，异步回答写回对应会话；切回加载中的会话保留思考中/完成后更新为回答

## 七、范围外 / 遗留

- 冷轨完整历史（含工具调用细节）仍为后台异步补齐：切换会话时主内容即时用内存态，工具过程等细节可能稍有延迟才补充显示（不阻塞即时切换）。
- 本修复未改动后端；后端冷轨异步写入与 `chat_messages` 存储不变。
- 若后续需要"同一用户多标签/多设备同步会话"，可在此 per-会话状态基础上演进（当前为单页标签内的内存态）。
