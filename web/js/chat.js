// ============ 会话 + 对话逻辑 ============
// 依赖 api.js / auth.js（已加载）。

let currentSessionId = null; // 当前选中会话（'' 表示未选择/尚未发送；chat 空 session_id 自动建会话）

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
    if (String(id) === String(currentSessionId)) {
      currentSessionId = null;
      renderMessages([]);
    }
    await loadSessions();
    toast('会话已删除', 'success');
  } catch (err) {
    toast(err.message, 'error');
  }
}

// 选择会话：加载完整历史（含工具调用，从冷轨 MySQL）
async function selectSession(id) {
  currentSessionId = String(id);
  // 高亮
  document.querySelectorAll('.session-item').forEach(function (el) {
    const active = String(el.getAttribute('data-id')) === currentSessionId;
    el.className = 'session-item group flex items-center justify-between px-3 py-2.5 rounded-lg cursor-pointer hover:bg-gray-100 ' +
      (active ? 'bg-indigo-50 border-l-4 border-indigo-500' : '');
  });
  await loadMessages();
}

// ---------- 对话历史渲染 ----------
async function loadMessages() {
  if (!currentSessionId) { renderMessages([]); return; }
  try {
    const data = await api.get('/session/' + currentSessionId + '/messages');
    renderMessages(data.messages || []);
  } catch (err) {
    renderMessages([{ kind: 'error', content: '加载历史失败: ' + err.message }]);
  }
}

function renderMessages(msgs) {
  const box = document.getElementById('messageBox');
  // 防御（P0 修复）：如果冷轨历史瞬时为空（异步写入滞后）且界面已有内容，
  // 不清空现有对话，避免"发送后整个界面被清空"。
  if ((!msgs || msgs.length === 0) && box.children.length > 0) {
    return;
  }
  box.innerHTML = '';
  if (!msgs || msgs.length === 0) {
    box.innerHTML = '<div class="text-gray-400 text-sm text-center py-16">开始对话吧，向你的企业知识库提问</div>';
    return;
  }
  msgs.forEach(function (m) {
    appendMessageByKind(m);
  });
  scrollToBottom();
}

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
      // kind 为空时按 role 兜底（后端约定）
      if (m.role === 'user') box.appendChild(buildBubble('user', m.content || ''));
      else if (m.role === 'assistant') box.appendChild(buildBubble('assistant', m.content || ''));
      else box.appendChild(buildToolCall('call', m.content || ''));
  }
}

// 普通消息气泡
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

// 工具调用过程气泡（可选加分项：展示 ReAct 推理过程）
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

// ---------- 发送消息 ----------
async function sendMessage() {
  const input = document.getElementById('chatInput');
  const query = input.value.trim();
  if (!query) return;
  if (document.getElementById('sendBtn').disabled) return;

  // 立即回显用户消息
  document.getElementById('messageBox').innerHTML = '';
  const box = document.getElementById('messageBox');
  if (currentSessionId) {
    // 有当前会话：先展示之前的完整历史再追加
    try {
      const data = await api.get('/session/' + currentSessionId + '/messages');
      (data.messages || []).forEach(function (m) { appendMessageByKind(m); });
    } catch (ignore) {}
  }
  box.appendChild(buildBubble('user', query));
  scrollToBottom();

  // 显示"思考中"
  const thinking = document.createElement('div');
  thinking.className = 'flex justify-start mb-4';
  thinking.innerHTML = '<div class="w-8 h-8 rounded-full bg-indigo-500 text-white flex items-center justify-center text-sm mr-2 shrink-0"><i class="fa-solid fa-robot"></i></div>' +
    '<div class="bg-white border px-4 py-3 rounded-2xl text-sm text-gray-500"><i class="fa-solid fa-spinner fa-spin mr-2"></i>思考中…</div>';
  box.appendChild(thinking);
  scrollToBottom();

  const btn = document.getElementById('sendBtn');
  btn.disabled = true;
  input.value = '';

  try {
    // chat：session_id 为空自动建会话
    const resp = await api.post('/chat', {
      session_id: currentSessionId,
      query: query,
    });
    const newSessionId = resp.session_id;
    if (!currentSessionId) {
      currentSessionId = newSessionId;
      loadSessions(); // 刷新左侧，出现新会话
    }
    // 移除"思考中"占位
    if (box.lastChild && box.lastChild === thinking) {
      box.removeChild(box.lastChild);
    }
    // 用 chat 响应实时渲染回答（不走冷轨历史——冷轨是异步后台写入，发送后立即读可能为空导致界面被清空）
    // 工具调用过程：chat 响应带 tool_calls（工具名列表），实时且可靠，展示为引导条；
    // 完整工具调用/结果细节在冷轨历史，切换/刷新会话时经 loadMessages 展示。
    const toolNames = Array.isArray(resp.tool_calls) ? resp.tool_calls : [];
    if (toolNames.length > 0) {
      box.appendChild(buildToolCall('call', '调用了工具：' + toolNames.join('、')));
    }
    box.appendChild(buildBubble('assistant', resp.answer || ''));
    scrollToBottom();
  } catch (err) {
    // 尽力移除 thinking（若仍在）
    if (box.lastChild && box.lastChild === thinking) {
      box.removeChild(box.lastChild);
    }
    box.appendChild(buildBubble('assistant', '出错了：' + err.message));
    scrollToBottom();
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
