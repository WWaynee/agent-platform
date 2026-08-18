// ============ API 请求封装 ============
// 统一处理鉴权、401 跳转、业务错误码（后端约定：成功 code=0 + HTTP 200；
// 业务错误如 400/403/500 一律 HTTP 200 + code!=0；仅鉴权失败是真正 HTTP 401）。
const API_BASE = '/api';

// request 通用请求：自动带 token；401 跳登录；code!=0 抛出带 message 的错误
async function request(path, options = {}) {
  options.headers = options.headers || {};
  // 默认 JSON
  if (options.body && typeof options.body === 'object' && !(options.body instanceof FormData)) {
    options.headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(options.body);
  }
  // 附带 token
  const token = getToken();
  if (token) {
    options.headers['Authorization'] = 'Bearer ' + token;
  }

  let res;
  try {
    res = await fetch(API_BASE + path, options);
  } catch (e) {
    throw new ApiError(-1, '网络错误，请检查后端服务是否可用', e);
  }

  // 401 → 鉴权失败：登录接口自身返回 401（用户名或密码错误等）不算"会话失效"，不跳转；
  // 业务页面收到 401 才是 token 失效，跳回登录页。此处解析 body 拿服务器 message 展示。
  if (res.status === 401) {
    let msg = '登录已失效，请重新登录';
    try {
      const body = await res.json();
      if (body && body.message) msg = body.message;
    } catch (e) { /* body 解析失败则用默认文案 */ }
    const onLoginPage = window.location.pathname.endsWith('/login.html');
    if (!onLoginPage) {
      clearAuth();
      window.location.href = '/login.html';
    }
    throw new ApiError(401, msg);
  }

  // 尝试解析 JSON（后端统一返回 {code, message, data}）
  let data = null;
  try {
    data = await res.json();
  } catch (e) {
    throw new ApiError(res.status, '响应解析失败: ' + res.status);
  }

  // 业务错误判断：HTTP 200 但 code!=0
  if (data === null || typeof data.code === 'undefined' || data.code !== 0) {
    const code = data && data.code !== undefined ? data.code : res.status;
    const msg = data && data.message ? data.message : '请求失败';
    throw new ApiError(code, msg, data && data.data);
  }

  return data.data; // 成功返回 data 字段
}

// get / post 便捷方法
const api = {
  get(path) { return request(path, { method: 'GET' }); },
  post(path, body) { return request(path, { method: 'POST', body: body || {} }); },
  put(path, body) { return request(path, { method: 'PUT', body: body || {} }); },
  del(path) { return request(path, { method: 'DELETE' }); },
  // FormData 上传（自动不加 Content-Type，浏览器自己带 boundary）
  upload(path, formData) {
    return request(path, { method: 'POST', body: formData });
  },
  request,
};

// 业务错误类：带 code、message、和原始 data
class ApiError extends Error {
  constructor(code, message, data) {
    super(message);
    this.code = code;
    this.data = data;
  }
}

// streamSSE 发起 SSE 流式请求（需求单 0009 全流程流式）。
// @param path   相对路径（如 /api...，内部会加 API_BASE）
// @param body   JSON 请求体
// @param handlers 事件回调 { event: (name, data) => void }
// @return Promise<{done:boolean}>（读取完毕即 resolve；HTTP/网络错误 reject）
// 事件名称由后端 text/event-stream 的 "event:" 指定；"data:" 是 JSON。
async function streamSSE(path, body, handlers) {
  const headers = { 'Content-Type': 'application/json' };
  const token = getToken();
  if (token) headers['Authorization'] = 'Bearer ' + token;

  let res;
  try {
    res = await fetch(API_BASE + path, {
      method: 'POST',
      headers: headers,
      body: JSON.stringify(body || {}),
    });
  } catch (e) {
    throw new ApiError(-1, '网络错误，连接对话流失败', e);
  }

  // 鉴权失败
  if (res.status === 401) {
    if (!window.location.pathname.endsWith('/login.html')) {
      clearAuth();
      window.location.href = '/login.html';
    }
    throw new ApiError(401, '登录已失效，请重新登录');
  }

  // 非 SSE（可能后端不支持流式或出错）→ 按普通 JSON 读，交给调用方兜底
  const contentType = (res.headers.get('content-type') || '');
  if (!res.body || contentType.indexOf('text/event-stream') < 0) {
    // 尝试读完整 JSON（{code,message,data}）
    let data = null;
    try { data = await res.json(); } catch (e) { data = null; }
    if (data && typeof data.code !== 'undefined' && data.code !== 0) {
      throw new ApiError(data.code, data.message || '请求失败', data.data);
    }
    return { raw: data, streamed: false };
  }

  // 正常 SSE：逐行读
  const reader = res.body.getReader();
  const decoder = new TextDecoder('utf-8');
  let buffer = '';
  const evt = (handlers && handlers.event) || function () {};

  // 解析一条 SSE：格式 event:<name>\ndata:<json>\n\n
  function dispatch(name, dataStr) {
    let payload = null;
    const t = dataStr.trim();
    if (t) {
      try { payload = JSON.parse(t); } catch (e) { payload = null; }
    }
    evt(name, payload);
  }

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    // 按空行切分事件块
    let idx;
    while ((idx = buffer.indexOf('\n\n')) >= 0) {
      const block = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 2);
      let name = '';
      let dataStr = '';
      const lines = block.split('\n');
      for (const line of lines) {
        if (line.startsWith('event:')) name = line.slice(6).trim();
        else if (line.startsWith('data:')) dataStr = line.slice(5);
        else if (line.startsWith(': ')) { /* comment / keep-alive 忽略 */ }
      }
      if (name || dataStr) dispatch(name, dataStr);
    }
  }
  // 尾部残留块
  if (buffer.trim()) {
    let name = ''; let dataStr = '';
    buffer.split('\n').forEach(function (line) {
      if (line.startsWith('event:')) name = line.slice(6).trim();
      else if (line.startsWith('data:')) dataStr = line.slice(5);
    });
    if (name || dataStr) dispatch(name, dataStr);
  }
  return { streamed: true, done: true };
}
