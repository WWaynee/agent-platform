// ============ 会话 + 对话逻辑 ============
// 依赖 api.js / auth.js（已加载）。
//
// 会话切换的正确性（修复 2026-08）：
//   - 每个会话在本页维护独立内存状态（history / pending），切换会话时**立即**从该状态渲染，
//     而非依赖冷轨异步历史（冷轨是异步后台写入，切换瞬间可能未落库，导致读空/串会话）。
//   - 发送消息是异步的：回答返回时若用户已切到别的会话，不直接操作当前 DOM，
//     而是把结果写回**对应的会话状态**；仅当该会话仍是当前会话时才渲染到界面。
//   - "加载中"状态按会话记录：切回加载中的会话，仍能显示"思考中…"，完成后更新为回答。

// sessions 内存会话状态：key = session id(string)，{ history: [], pending: bool }
let sessions = {};
// currentSessionId 当前选中会话（null 表示尚未选择/发送；chat 空 session_id 自动建会话）
let currentSessionId = null;

// ---------- 会话列表 ----------
async function loadSessions() {
  const el = document.getElementById('sessionList');
  try {
    const data = await api.get('/session/list?page=1&page_size=100');
    const list = data.list || [];
    if (list.length === 0) {
      el.innerHTML = '<div class="text-gray-400 text-xs text-center py-6">还没有会话<br/>点击左上角新建一个吧</div>';
      return;
    }
    el.innerHTML = '';
    list.forEach(function (s) {
      const item = document.createElement('div');
      item.className = 'session-item group flex items-center justify-between px-3 py-2.5 rounded-lg cursor-pointer hover:bg-gray-100 ' +
        (String(s.ID) === String(currentSessionId) ? 'bg-indigo-50 border-l-4 border-indigo-500' : '');
      item.setAttribute('data-id', s.ID);

      // 标题 + 删除按钮
      item.innerHTML =
        '<div class="flex-1 min-w-0">' +
          '<div class="text-gray-800 text-sm truncate">' + escapeHtml(itemTitle(s)) + '</div>' +
          '<div class="text-gray-400 text-xs mt-0.5">' + formatTime(s.UpdatedAt) + '</div>' +
        '</div>' +
        '<button class="text-gray-300 hover:text-red-500 opacity-0 group-hover:opacity-100 ml-2" title="删除会话" onclick="event.stopPropagation(); deleteSession(' + s.ID + ')">' +
          '<i class="fa-solid fa-trash-can"></i>' +
        '</button>';

      item.addEventListener('click', function () { selectSession(s.ID); });
      el.appendChild(item);
    });

    // 若当前未选中任何会话（页面初始 / 无选中），默认进入列表中最新的会话（列表按更新时间倒序）
    if (currentSessionId === null || currentSessionId === undefined) {
      await selectSession(list[0].ID);
    }
  } catch (err) {
    el.innerHTML = '<div class="text-red-500 text-xs text-center py-4">加载会话失败<br/>' + escapeHtml(err.message) + '</div>';
  }
}

function itemTitle(s) {
  return (s.Title && s.Title.trim()) ? s.Title : ('会话 ' + s.ID);
}

async function createSession() {
  try {
    await api.post('/session', { title: '' });
    await loadSessions();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function deleteSession(id) {
  if (!confirm('确定删除该会话及其全部消息历史？删除后不可恢复。')) return;
  try {
    await api.del('/session/' + id);
    delete sessions[String(id)];
    if (String(id) === String(currentSessionId)) {
      currentSessionId = null;
      renderCurrent();
    }
    await loadSessions();
    toast('会话已删除', 'success');
  } catch (err) {
    toast(err.message, 'error');
  }
}

// 会话状态工具
function sessionState(id) {
  const key = String(id);
  if (!sessions[key]) sessions[key] = { history: [], pending: false };
  return sessions[key];
}

// ---------- 选择会话：立即切换到该会话内容 ----------
async function selectSession(id) {
  const wanted = String(id);
  currentSessionId = wanted;
  // 高亮左侧
  document.querySelectorAll('.session-item').forEach(function (el) {
    const active = String(el.getAttribute('data-id')) === currentSessionId;
    el.className = 'session-item group flex items-center justify-between px-3 py-2.5 rounded-lg cursor-pointer hover:bg-gray-100 ' +
      (active ? 'bg-indigo-50 border-l-4 border-indigo-500' : '');
  });
  // 立即渲染该会话（含加载中/完成状态）；
  // 冷轨完整历史（含工具调用细节）可能有异步滞后，这里在后台静默补充，
  // 但不阻塞"立即切换"（用内存状态先渲染，查冷轨仅是补齐工具过程等细节）。
  renderCurrent();
  // 若该会话尚未从冷轨加载过历史且非进行中，后台异步补齐冷轨历史
  const st = sessionState(wanted);
  if (!st.loaded && !st.pending) {
    st.loaded = true; // 标记已尝试加载，避免重复请求
    loadMessagesInto(wanted);
  }
}

// ---------- 渲染当前会话 ----------
function renderCurrent() {
  const box = document.getElementById('messageBox');
  if (!currentSessionId) {
    box.innerHTML = '<div class="text-gray-400 text-sm text-center py-16">开始对话吧，向你的企业知识库提问</div>';
    return;
  }
  const st = sessionState(currentSessionId);
  // 渲染历史 + 若进行中追加"思考中"占位
  const msgs = st.history;
  box.innerHTML = '';
  if (!msgs || msgs.length === 0) {
    box.innerHTML = '<div class="text-gray-400 text-sm text-center py-16">开始对话吧，向你的企业知识库提问</div>';
  } else {
    msgs.forEach(function (m) { appendMessageByKind(m); });
  }
  if (st.pending) {
    box.appendChild(buildThinking());
  }
  scrollToBottom();
}

// 追加一条消息到当前会话状态并渲染（仅当是当前会话）
function pushToSession(sid, m) {
  const st = sessionState(sid);
  st.history.push(m);
  // 若该会话正是当前会话，实时追加渲染
  if (String(sid) === String(currentSessionId)) {
    const box = document.getElementById('messageBox');
    // 移除末尾"思考中"占位
    removeThinkingFromBox(box);
    appendMessageByKind(m);
    scrollToBottom();
  }
}

function removeThinkingFromBox(box) {
  // 移除所有 thinking 气泡（不只看最后一个：可能已追加了 tool_call 等）
  if (!box || !box.children) return;
  for (var i = box.children.length - 1; i >= 0; i--) {
    var child = box.children[i];
    if (child && child.classList && child.classList.contains('thinking-bubble')) {
      box.removeChild(child);
    }
  }
}

function buildThinking() {
  const div = document.createElement('div');
  div.className = 'flex justify-start mb-4 thinking-bubble';
  div.innerHTML = '<div class="w-8 h-8 rounded-full bg-indigo-500 text-white flex items-center justify-center text-sm mr-2 shrink-0"><i class="fa-solid fa-robot"></i></div>' +
    '<div class="bg-white border px-4 py-3 rounded-2xl text-sm text-gray-500"><i class="fa-solid fa-spinner fa-spin mr-2"></i>思考中…</div>';
  return div;
}

// 后台把冷轨完整历史合并进某会话状态（补齐工具调用细节）。仅供切换/刷新时改善展示，不阻塞。
async function loadMessagesInto(sid) {
  if (!sid) return;
  try {
    const data = await api.get('/session/' + sid + '/messages');
    const list = data.messages || [];
    // 仅当该会话没有本地新内容（未发送/无未完成加载）时才用冷轨整体覆盖，
    // 避免迟到的冷轨（仍在校验中）覆盖掉刚渲染的最新回答。
    const st = sessionState(sid);
    if (!st.pending) {
      // 若内存历史为空或冷轨历史比内存长，则以冷轨为准补齐；否则保留内存的"本地已送达"内容
      if (st.history.length === 0 || list.length > st.history.length) {
        // 冷轨为空时不要覆盖（保留内存已有内容），空则保持空
        if (list.length > 0 || st.history.length === 0) {
          st.history = list;
        }
        st.loaded = true;
        if (String(sid) === String(currentSessionId)) {
          renderCurrent();
        }
      }
    }
  } catch (e) { /* 读冷轨失败静默，不阻塞界面 */ }
}

// ---------- 发送消息 ----------
async function sendMessage() {
  const input = document.getElementById('chatInput');
  const query = input.value.trim();
  if (!query) return;
  if (document.getElementById('sendBtn').disabled) return;

  const atSession = currentSessionId; // 记录发起时的会话
  const st = sessionState(atSession);
  const box = document.getElementById('messageBox');

  // 若这是新会话（currentSessionId 为 null）：自动创建
  // 先回显用户消息到状态
  pushToSession(atSession, { kind: 'question', content: query, role: 'user' });

  // 标记该会话"进行中"，并显示思考中
  st.pending = true;
  // 当前如果是该会话，确保界面显示思考中
  if (String(atSession) === String(currentSessionId)) {
    // 去掉可能残留的 thinking
    removeThinkingFromBox(box);
    box.appendChild(buildThinking());
    scrollToBottom();
  }

  const btn = document.getElementById('sendBtn');
  btn.disabled = true;
  input.value = '';

  // 流式过程：完整回答、工具调用清单、会话ID在 done 事件里收集
  let fullAnswer = '';
  let toolNameList = [];
  let finalSessionId = '';
  let streamingSucceeded = false;
  // 流式过程中收到的工具调用/结果（按顺序），done 后统一写回会话历史，保证 rebuild 后保留
  const streamEvents = [];
  // 打字机：当前 answer 气泡 DOM 引用（仅当还在当前会话时操作）
  let answerEl = null;

  // 若仍在发起会话，确保一个逐字渲染的 answer 气泡存在并返回它
  // 创建 answer 气泡前先把"思考中"占位移除，避免答案显示在思考框之后。
  function ensureAnswerEl() {
    if (String(atSession) === String(currentSessionId) && !answerEl) {
      removeThinkingFromBox(box);
      answerEl = buildBubble('assistant', '');
      box.appendChild(answerEl);
    }
    return answerEl;
  }

  try {
    const result = await streamSSE('/chat', {
      session_id: atSession ? atSession : null,
      query: query,
      stream: true,
    }, {
      event: function (name, data) {
        switch (name) {
          case 'thinking':
            // 维持"思考中"占位（已显示），不额外处理
            break;
          case 'tool_call':
            // 记录到流式事件（done 后写会话历史），并实时渲染工具调用条
            streamEvents.push({ kind: 'tool_call', content: (data && data.tool ? data.tool : '') , role: 'tool' });
            if (String(atSession) === String(currentSessionId)) {
              removeThinkingFromBox(box);
              const tc = buildToolCall('call', data && data.message ? data.message : (data && data.tool ? '正在调用 ' + data.tool + ' 工具…' : ''));
              box.appendChild(tc);
              scrollToBottom();
            }
            break;
          case 'tool_result':
            // 记录到流式事件（done 后写会话历史），并实时渲染工具返回（折叠展示）
            streamEvents.push({ kind: 'tool_result', content: (data && data.result ? data.result : ''), role: 'tool' });
            if (String(atSession) === String(currentSessionId)) {
              const tr = buildToolCall('result', data && data.result ? data.result : '');
              box.appendChild(tr);
              scrollToBottom();
            }
            break;
          case 'answer_token':
            // 逐字打字机：先移除"思考中"占位，再拼字到回答气泡
            fullAnswer += (data && data.delta) || '';
            const el = ensureAnswerEl();
            if (el) {
              el.querySelector('.bubble-text').textContent = fullAnswer;
              scrollToBottom();
            }
            break;
          case 'done':
            fullAnswer = (data && data.answer) || fullAnswer;
            finalSessionId = data && data.session_id ? String(data.session_id) : finalSessionId;
            if (data && Array.isArray(data.tool_calls)) toolNameList = data.tool_calls;
            streamingSucceeded = true;
            break;
        }
      }
    });

    // 收集完毕：确定目标会话
    const newSessionId = finalSessionId || String(result && result.raw && result.raw.session_id ? result.raw.session_id : '');
    const targetId = atSession ? String(atSession) : (newSessionId || currentSessionId || 'default');

    // 新会话首次发送：迁移状态
    if (!atSession && newSessionId) {
      sessions[newSessionId] = sessions['null'] || { history: [], pending: false };
      delete sessions['null'];
      if (currentSessionId === null || currentSessionId === undefined) {
        currentSessionId = newSessionId;
        loadSessions();
      }
    }

    // 写回目标会话状态：question + 工具调用/结果（streamEvents） + answer，保持真实时序
    const targetSt = sessionState(targetId);
    targetSt.pending = false;
    for (var si = 0; si < streamEvents.length; si++) {
      targetSt.history.push(streamEvents[si]);
    }
    targetSt.history.push({ kind: 'answer', content: fullAnswer || '', role: 'assistant' });

    // 仅当仍是当前会话才整体重渲染（确保与状态一致）
    if (String(targetId) === String(currentSessionId)) {
      renderCurrent();
    }
    loadSessions();
  } catch (err) {
    // 失败：移除 thinking（若还在），写错误到目标会话
    const cur = atSession ? String(atSession) : (currentSessionId || 'default');
    const targetSt = sessionState(cur);
    targetSt.pending = false;
    if (!atSession && sessions['null']) sessions['null'].pending = false;
    targetSt.history.push({ kind: 'answer', content: '出错了：' + err.message, role: 'assistant' });
    if (String(cur) === String(currentSessionId)) {
      renderCurrent();
    }
  } finally {
    btn.disabled = false;
    input.focus();
  }
}

function scrollToBottom() {
  const box = document.getElementById('messageBox');
  box.scrollTop = box.scrollHeight;
}

function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, function (m) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[m];
  });
}

function formatTime(t) {
  if (!t) return '';
  const d = new Date(t);
  if (isNaN(d.getTime())) return t;
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  return sameDay ?
    String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0') :
    (d.getMonth() + 1) + '-' + d.getDate();
}

// ---------- 渲染辅助（bubble / toolcall / 按 kind 追加） ----------
function appendMessageByKind(m) {
  const box = document.getElementById('messageBox');
  const kind = m.kind || '';
  switch (kind) {
    case 'question':
      box.appendChild(buildBubble('user', m.content || ''));
      break;
    case 'answer':
      box.appendChild(buildBubble('assistant', m.content || ''));
      break;
    case 'tool_call':
      box.appendChild(buildToolCall('call', m.content || ''));
      break;
    case 'tool_result':
      box.appendChild(buildToolCall('result', m.content || ''));
      break;
    case 'system':
      box.appendChild(buildToolCall('system', m.content || ''));
      break;
    default:
      if (m.role === 'user') box.appendChild(buildBubble('user', m.content || ''));
      else if (m.role === 'assistant') box.appendChild(buildBubble('assistant', m.content || ''));
      else box.appendChild(buildToolCall('call', m.content || ''));
  }
}

function buildBubble(role, text) {
  const isUser = role === 'user';
  const div = document.createElement('div');
  div.className = 'flex ' + (isUser ? 'justify-end' : 'justify-start') + ' mb-4';
  div.innerHTML =
    (isUser ? '' : '<div class="w-8 h-8 rounded-full bg-indigo-500 text-white flex items-center justify-center text-sm mr-2 shrink-0"><i class="fa-solid fa-robot"></i></div>') +
    '<div class="max-w-[75%] px-4 py-3 rounded-2xl text-sm shadow-sm break-words ' +
      (isUser ? 'bg-indigo-600 text-white rounded-br-sm' : 'bg-white text-gray-800 border rounded-bl-sm') + '">' +
      '<span class="bubble-text">' + escapeHtml(text) + '</span></div>';
  return div;
}

// 工具返回预览截断阈值（字符）：超过则折叠显示，点击展开/收起
const TOOL_RESULT_PREVIEW_CHARS = 100;

function buildToolCall(type, content) {
  const div = document.createElement('div');
  const isCall = type === 'call';
  div.className = 'flex justify-center mb-2';
  const icon = isCall ? 'fa-wand-magic-sparkles' : (type === 'system' ? 'fa-box-archive' : 'fa-receipt');
  const color = isCall ? 'text-sky-600' : (type === 'system' ? 'text-gray-500' : 'text-amber-600');
  const isResult = type === 'result';

  div.innerHTML =
    '<div class="w-full max-w-[600px] bg-gray-50 border border-gray-200 rounded-lg px-3 py-2 text-xs flex items-start gap-2 ' + color + '">' +
      '<i class="fa-solid ' + icon + ' mt-0.5 shrink-0"></i>' +
      '<div class="flex-1 min-w-0 flex flex-col">' +
        '<div class="text-gray-500 mb-0.5">' +
          (isCall ? '🔧 正在调用工具…' : (type === 'system' ? '系统消息：' : '📄 工具返回：')) +
        '</div>' +
        '<div class="tool-result-body whitespace-pre-wrap break-words"></div>' +
        (isResult ? '<button class="tool-result-toggle self-end mt-1 text-sky-600 hover:text-sky-800 font-medium"></button>' : '') +
      '</div>' +
    '</div>';

  if (isResult) {
    // tool_result：默认截断预览 + 点击展开/收起完整内容
    const full = String(content == null ? '' : content);
    const bodyEl = div.querySelector('.tool-result-body');
    const toggleEl = div.querySelector('.tool-result-toggle');
    let expanded = false;

    function renderResult() {
      if (expanded) {
        bodyEl.textContent = full;
        toggleEl.textContent = '▲ 收起';
      } else {
        bodyEl.textContent = full.length > TOOL_RESULT_PREVIEW_CHARS
          ? full.slice(0, TOOL_RESULT_PREVIEW_CHARS) + '…'
          : full;
        toggleEl.style.display = full.length > TOOL_RESULT_PREVIEW_CHARS ? '' : 'none';
        if (full.length > TOOL_RESULT_PREVIEW_CHARS) toggleEl.textContent = '▼ 展开';
      }
    }
    toggleEl.addEventListener('click', function () {
      expanded = !expanded;
      renderResult();
    });
    renderResult();
  } else {
    // tool_call / system：保持完整展示
    const bodyEl = div.querySelector('.tool-result-body');
    bodyEl.textContent = String(content == null ? '' : content);
  }
  return div;
}
