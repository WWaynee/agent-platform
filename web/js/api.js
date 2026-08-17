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
