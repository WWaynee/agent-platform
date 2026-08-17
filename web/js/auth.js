// ============ 鉴权与登录态管理 ============
// 依赖 api.js 中的 request()，先加载 api.js 再加载 auth.js。

const TOKEN_KEY = 'token';
const USER_KEY = 'auth_user';      // 缓存 user（username/tenant_id/role/user_id）
const TENANT_NAME_KEY = 'tenant_name';

function getToken() {
  return localStorage.getItem(TOKEN_KEY);
}

function setToken(token) {
  localStorage.setItem(TOKEN_KEY, token);
}

function getUser() {
  try {
    return JSON.parse(localStorage.getItem(USER_KEY) || 'null');
  } catch (e) {
    return null;
  }
}

function setUser(user) {
  localStorage.setItem(USER_KEY, JSON.stringify(user));
}

function getTenantName() {
  return localStorage.getItem(TENANT_NAME_KEY) || '';
}

function setTenantName(name) {
  localStorage.setItem(TENANT_NAME_KEY, name || '');
}

// 判断是否已登录（本地有 token 即视为登录态）
function isLoggedIn() {
  return !!getToken();
}

// 需要登录的页面：无 token 跳回登录页
function requireAuth() {
  if (!isLoggedIn()) {
    window.location.href = '/login.html';
    return false;
  }
  return true;
}

// 角色是否为管理员
// 后端 model.User 无 json tag，字段名为大写 Role
function isAdmin() {
  const u = getUser();
  return u && u.Role === 'admin';
}

// 退出登录：清空本地登录态，跳登录页
function logout() {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
  localStorage.removeItem(TENANT_NAME_KEY);
  window.location.href = '/login.html';
}

function clearAuth() {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
  localStorage.removeItem(TENANT_NAME_KEY);
}

// 登录成功后：保存 token + user + 拉取租户名缓存（供顶栏展示）
// ⚠️ 后端 model.User/model.Tenant 无 json tag，字段名为大写（Username/Role/TenantID/Name），与 gin.H 的小写字段不同。
async function afterLogin(data) {
  // data: {token, user}
  setToken(data.token);
  setUser(data.user);
  // 拉取租户名（登录响应 user 只有 tenant_id，需再查一次租户详情）
  try {
    const uid = data.user && data.user.TenantID;
    const tenant = await api.get('/tenant/' + uid);
    const name = (tenant && tenant.Name) ? tenant.Name : ('租户 ' + uid);
    setTenantName(name);
  } catch (e) {
    // 拉租户名失败不影响登录
    setTenantName('租户 ' + (data.user ? data.user.TenantID : ''));
  }
}
