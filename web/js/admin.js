// ============ 管理后台逻辑 ============
// 依赖 api.js / auth.js（已加载）。仅管理员角色可访问（页面已做角色拦截）。

// 加载工具开关列表
async function loadTools() {
  const el = document.getElementById('toolList');
  try {
    const data = await api.get('/admin/tool-config');
    const list = data.list || [];
    if (list.length === 0) {
      el.innerHTML = '<div class="text-gray-400 text-sm text-center py-8">暂无可配置的工具</div>';
      return;
    }
    el.innerHTML = '';
    list.forEach(function (t) {
      el.appendChild(buildToolRow(t));
    });
  } catch (err) {
    el.innerHTML = '<div class="text-red-500 text-sm text-center py-8">加载工具配置失败：' + escapeHtml(err.message) + '</div>';
  }
}

function buildToolRow(t) {
  const div = document.createElement('div');
  div.className = 'flex items-center justify-between px-4 py-3 border-b border-gray-100 last:border-0';
  const enabled = !!t.is_enable;
  div.innerHTML =
    '<div class="min-w-0 flex-1">' +
      '<div class="text-sm text-gray-800 font-medium">' + escapeHtml(t.tool_name) + '</div>' +
      '<div class="text-xs text-gray-400 mt-0.5 truncate" title="' + escapeHtml(t.description || '') + '">' + escapeHtml(t.description || '') + '</div>' +
    '</div>' +
    '<div class="ml-3 shrink-0">' +
      // toggle 开关
      '<button onclick="toggleTool(\'' + escapeHtml(t.tool_name) + '\', ' + (!enabled) + ')" class="w-11 h-6 rounded-full transition relative ' +
        (enabled ? 'bg-indigo-600' : 'bg-gray-300') + '">' +
        '<span class="absolute top-0.5 w-5 h-5 bg-white rounded-full transition ' + (enabled ? 'left-[calc(100%-1.375rem)]' : 'left-0.5') + '"></span>' +
      '</button>' +
    '</div>';
  return div;
}

// 开关某个工具
async function toggleTool(name, nextEnable) {
  // nextEnable 是切换后的目标状态（当前按钮是取反后的值）
  try {
    await api.put('/admin/tool-config/' + encodeURIComponent(name), { is_enable: nextEnable });
    toast('工具「' + name + '」已' + (nextEnable ? '启用' : '禁用'), 'success');
    loadTools();
  } catch (err) {
    toast('操作失败：' + err.message, 'error');
    loadTools(); // 回滚显示真实状态
  }
}

// 用量统计：当天 + 最近 7 天
async function loadUsage() {
  try {
    // 当天
    const today = await api.get('/admin/usage/today');
    document.getElementById('todayTokens').textContent = formatNum(today.tokens);
    document.getElementById('todayCalls').textContent = formatNum(today.calls);

    // 最近 7 天
    const hist = await api.get('/admin/usage/history?days=7');
    const list = hist.list || [];
    // 柱状图：百分比基于最大 token 值
    const maxToken = list.reduce(function (m, it) { return Math.max(m, it.tokens || 0); }, 0);
    const barWrap = document.getElementById('usageBars');
    barWrap.innerHTML = '';
    list.forEach(function (it) {
      const pct = maxToken > 0 ? Math.round(((it.tokens || 0) / maxToken) * 100) : 0;
      barWrap.innerHTML +=
        '<div class="flex items-center gap-3 mb-2">' +
          '<div class="w-14 text-xs text-gray-500 text-right shrink-0">' + escapeHtml(it.date || '') + '</div>' +
          '<div class="flex-1 bg-gray-100 rounded h-5 overflow-hidden">' +
            '<div class="bg-indigo-500 h-full rounded" style="width:' + pct + '%"></div>' +
          '</div>' +
          '<div class="w-20 text-xs text-gray-600 shrink-0">' + formatNum(it.tokens) + ' tok</div>' +
        '</div>';
    });
    if (list.length === 0) {
      barWrap.innerHTML = '<div class="text-gray-400 text-sm text-center py-4">暂无用量数据</div>';
    }
    document.getElementById('usageDays').textContent = list.length + ' 天';
  } catch (err) {
    toast('用量统计加载失败：' + err.message, 'error');
  }
}

function formatNum(n) {
  if (n == null) return '0';
  if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M';
  if (n >= 1e4) return (n / 1e3).toFixed(1) + 'K';
  return String(n);
}

function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, function (m) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[m];
  });
}

// toast（管理页复用）
function toast(text, type) {
  const container = document.getElementById('toastContainer');
  if (!container) { alert(text); return; }
  const div = document.createElement('div');
  const color = type === 'success' ? 'bg-green-500' : (type === 'error' ? 'bg-red-500' : 'bg-gray-800');
  const icon = type === 'success' ? 'fa-circle-check' : (type === 'error' ? 'fa-circle-exclamation' : 'fa-info-circle');
  div.className = 'text-white text-sm px-4 py-3 rounded-lg shadow-lg flex items-center gap-2 ' + color;
  div.innerHTML = '<i class="fa-solid ' + icon + '"></i><span>' + escapeHtml(String(text)) + '</span>';
  container.appendChild(div);
  setTimeout(function () { div.remove(); }, 3500);
}
