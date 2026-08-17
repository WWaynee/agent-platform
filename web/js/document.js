// ============ 文档管理逻辑 ============
// 依赖 api.js / auth.js（已加载）。

// 轮询中的任务 map：task_id -> 定时器
const taskTimers = {};

async function loadDocuments() {
  const el = document.getElementById('docList');
  try {
    const data = await api.get('/document/list?page=1&page_size=100');
    const list = data.list || [];
    if (list.length === 0) {
      el.innerHTML = '<div class="text-gray-400 text-xs text-center py-6">还没有上传文档</div>';
      return;
    }
    el.innerHTML = '';
    list.forEach(function (d) {
      el.appendChild(buildDocItem(d));
    });
  } catch (err) {
    el.innerHTML = '<div class="text-red-500 text-xs text-center py-4">加载文档失败<br/>' + escapeHtml(err.message) + '</div>';
  }
}

function buildDocItem(d) {
  const div = document.createElement('div');
  div.className = 'flex items-center justify-between px-3 py-2.5 border-b border-gray-100 last:border-0';
  div.innerHTML =
    '<div class="flex items-center gap-2 min-w-0 flex-1">' +
      '<i class="fa-solid fa-file-lines text-gray-400 shrink-0"></i>' +
      '<div class="min-w-0 flex-1">' +
        '<div class="text-sm text-gray-800 truncate" title="' + escapeHtml(d.Name) + '">' + escapeHtml(d.Name) + '</div>' +
        '<div class="flex items-center gap-2 mt-0.5">' +
          '<span class="text-xs ' + statusClass(d.Status) + '">' + statusText(d.Status) + '</span>' +
          '<span class="text-xs text-gray-400">' + fmtSize(d.Size) + '</span>' +
        '</div>' +
        (d.Status === 'failed' && d.ErrorMsg ? '<div class="text-xs text-red-500 mt-0.5 truncate">' + escapeHtml(d.ErrorMsg) + '</div>' : '') +
      '</div>' +
    '</div>' +
    '<button class="text-gray-300 hover:text-red-500 ml-2" title="删除文档" onclick="deleteDocument(' + d.ID + ')">' +
      '<i class="fa-solid fa-trash-can"></i>' +
    '</button>';
  return div;
}

function statusText(s) {
  switch (s) {
    case 'pending': return '待处理';
    case 'processing': return '处理中…';
    case 'success': return '已完成';
    case 'failed': return '处理失败';
    default: return s;
  }
}
function statusClass(s) {
  switch (s) {
    case 'success': return 'text-green-600';
    case 'failed': return 'text-red-600';
    case 'processing': return 'text-amber-600';
    default: return 'text-gray-400';
  }
}
function fmtSize(bytes) {
  if (!bytes && bytes !== 0) return '';
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(2) + ' MB';
}

// 上传文档（input 选择后），返回 File
async function doUpload(file) {
  if (!file) return;
  const formData = new FormData();
  formData.append('file', file);
  const btn = document.getElementById('uploadBtn');
  btn.disabled = true;
  try {
    toast('开始上传「' + file.name + '」…', 'info');
    const data = await api.upload('/document/upload', formData);
    // data.task_id：轮询任务状态
    if (data.task_id) {
      pollTask(data.task_id);
    }
    toast('上传成功，后台异步处理中…', 'success');
  } catch (err) {
    toast('上传失败：' + err.message, 'error');
  } finally {
    btn.disabled = false;
    // 重置 input，允许重复选择同一文件
    document.getElementById('fileInput').value = '';
  }
}

// 轮询任务状态（间隔 2s，防撞限流；success/failed 停止）
function pollTask(taskId) {
  if (taskTimers[taskId]) clearInterval(taskTimers[taskId]);
  taskTimers[taskId] = setInterval(async function () {
    try {
      const task = await api.get('/task/' + taskId);
      if (task.status === 'success' || task.status === 'failed') {
        clearInterval(taskTimers[taskId]);
        delete taskTimers[taskId];
        toast(task.status === 'success' ? '文档处理完成' : '文档处理失败', task.status === 'success' ? 'success' : 'error');
        loadDocuments(); // 完成/失败都刷新列表
      } else {
        // processing / pending：只更新对应文档状态提示（这里简单刷新列表显示状态）
        loadDocuments();
      }
    } catch (err) {
      // 429 限流或临时错误：不中断轮询，退避重试
      if (err.code !== 429) {
        // 其它错误（如任务不存在）停止轮询避免死循环
        clearInterval(taskTimers[taskId]);
        delete taskTimers[taskId];
      }
    }
  }, 2000);
}

// 删除文档（仅上传者本人可删，后端校验）
async function deleteDocument(id) {
  if (!confirm('确定删除该文档？将同时删除其向量索引。')) return;
  try {
    await api.del('/document/' + id);
    toast('文档已删除', 'success');
    loadDocuments();
  } catch (err) {
    toast('删除失败：' + err.message, 'error');
  }
}

// 拖拽上传支持
function setupDragDrop() {
  const zone = document.getElementById('uploadZone');
  ['dragover', 'dragenter'].forEach(function (ev) {
    zone.addEventListener(ev, function (e) {
      e.preventDefault();
      zone.classList.add('border-indigo-500', 'bg-indigo-50');
    });
  });
  ['dragleave', 'drop'].forEach(function (ev) {
    zone.addEventListener(ev, function (e) {
      e.preventDefault();
      zone.classList.remove('border-indigo-500', 'bg-indigo-50');
    });
  });
  zone.addEventListener('drop', function (e) {
    const files = e.dataTransfer.files;
    if (files && files.length) doUpload(files[0]);
  });
}

function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, function (m) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[m];
  });
}
