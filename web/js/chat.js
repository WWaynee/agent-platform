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
  const last = box.lastChild;
  if (last && last.classList && last.classList.contains('thinking-bubble')) {
    box.removeChild(last);
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

  // resp 在 try/catch 间共享：请求可能失败，catch 需要知道 session 归属性
  let resp = null;

  try {
    resp = await api.post('/chat', {
      session_id: atSession ? atSession : null,
      query: query,
    });
    const newSessionId = String(resp.session_id);
    // 目标会话：发送前有会话（atSession）则写回该会话；发送前无会话（新会话）则用后端返回的 newSessionId
    const targetId = atSession ? String(atSession) : newSessionId;

    // 若是新会话首次发送（atSession 为 null）：
    //   - 建立 newSessionId 的会话状态（把 origin，即在占位 key('null') 里暂存的用户消息迁过来）；
    //   - 仅当发送期间用户没有切走（currentSessionId 仍为 null）才把当前视图绑定到新会话；
    //     若用户已手动切到别的会话则不抢占视图（只落状态，切回时可见）。
    if (!atSession) {
      sessions[newSessionId] = sessions['null'] || { history: [], pending: false };
      delete sessions['null'];
      if (currentSessionId === null || currentSessionId === undefined) {
        currentSessionId = newSessionId;
        loadSessions(); // 刷新左侧出现新会话
      }
    }

    // 结果写回目标会话状态（无论当前是否还显示该会话），目标恒定 = 发起会话
    const targetSt = sessionState(targetId);
    targetSt.pending = false;
    // 工具调用引导条
    const toolNames = Array.isArray(resp.tool_calls) ? resp.tool_calls : [];
    if (toolNames.length > 0) {
      targetSt.history.push({ kind: 'tool_call', content: '调用了工具：' + toolNames.join('、'), role: 'tool' });
    }
    targetSt.history.push({ kind: 'answer', content: resp.answer || '', role: 'assistant' });

    // 仅当发起会话仍是当前查看会话时才实时渲染 DOM；否则只更新状态，切回时 renderCurrent 会显示
    if (String(targetId) === String(currentSessionId)) {
      renderCurrent();
    }
    // 刷新会话列表（更新时间/标题）
    loadSessions();
  } catch (err) {
    // 失败也写回目标会话：有 atSession 用发起会话；否则优先请求返回的 session_id；再退到当前会话兜底
    const cur = atSession ? String(atSession) : (String(resp && resp.session_id ? resp.session_id : '') || (currentSessionId || 'default'));
    const targetSt = sessionState(cur);
    targetSt.pending = false;
    // 新会话首条失败：占位 key('null') 里暂存的用户消息也需复位 pending，避免残留思考态
    if (!atSession && sessions['null']) {
      sessions['null'].pending = false;
    }
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
      escapeHtml(text) + '</div>';
  return div;
}

function buildToolCall(type, content) {
  const div = document.createElement('div');
  const isCall = type === 'call';
  div.className = 'flex justify-center mb-2';
  const icon = isCall ? 'fa-wand-magic-sparkles' : (type === 'system' ? 'fa-box-archive' : 'fa-receipt');
  const color = isCall ? 'text-sky-600' : (type === 'system' ? 'text-gray-500' : 'text-amber-600');
  div.innerHTML =
    '<div class="max-w-[85%] bg-gray-50 border border-gray-200 rounded-lg px-3 py-2 text-xs flex items-start gap-2 ' + color + '">' +
      '<i class="fa-solid ' + icon + ' mt-0.5"></i>' +
      '<div class="flex-1 min-w-0"><div class="text-gray-500 mb-0.5">' +
        (isCall ? '🔧 正在调用工具…' : (type === 'system' ? '系统消息：' : '📄 工具返回：')) +
      '</div>' + escapeHtml(content) + '</div>' +
    '</div>';
  return div;
}
