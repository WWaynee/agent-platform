# 需求单 0007（feature）：管理员在管理后台创建普通用户（员工）

- 类型：✨ **feature**（功能需求；0001-0004、0007 为 feature，0005-0006 为 bugfix）
- 状态：✅ **已实现并验证**（2026-08-19 实现 + 提权口子一并堵上 + 单测/全量回归 + 独立 review ship；已提交推送 `origin/main`）
- 优先级：🟡 中（补齐"普通用户如何进入平台"的闭环——之前只有建租户+管理员，缺少普通员工创建入口；同时修复一个提权隐患）
- 模块：`api/handler/admin_user.go`（新增）、`api/router.go`、`api/service/user.go`、`api/service/account_test.go`、`web/admin.html`、`web/js/admin.js`
- 创建日期：2026-08-19
- 完成日期：2026-08-19

---

## 一、需求背景 / 现状

平台此前只有两个账号入口：

1. **公开注册租户**（`POST /api/tenant/register`）：原子创建「租户 + 首个管理员 admin + 默认工具配置」。
2. **公开注册用户**（`POST /api/user/register`）：向已存在租户注册用户。

**缺失**：没有一个让"租户的普通员工（member）进入平台"的受控入口。同时暴露出一个**提权隐患**——原 `POST /api/user/register` 的请求体允许传 `role=admin`，且 `service.Register` 未强制 member，导致**任何知道某租户 `tenant_id` 的人（建租户接口是公开的、会返回租户 ID）都能给自己造一个管理员账号**，破坏"建租户时首个 admin 固定 role=admin、不可由调用方传 role 提权"的初衷。

## 二、设计决策（与需求方确认）

需求方确认采用：
1. **普通员工由该租户的管理员在管理后台创建**（而非公开自助注册）。
2. 管理员**只能给自己所属租户**建员工（租户归属由 JWT 决定，不允许选择/自报，堵跨租户越权）。
3. 创建的角色**固定为 member**（普通员工），本入口不提供建管理员能力。
4. 顺手**堵掉公开注册的提权口子**（强制 member）。

## 三、方案

### 3.1 后端

**新增管理员专属接口 `POST /api/admin/user`（创建员工）**：

- 路由挂 `private.Group("/admin")` + `AdminAuth()`（JWT 先鉴权 → 再 admin 角色校验，仅当前租户的管理员可调）。
- 请求体 `{ username, password }`：
  - **不定义 `tenant_id` 字段**（由 handler 从 JWT 的 `GetTenantID(c)` 取 = 管理员所属租户，**不信前端 body**，防跨租户越权）;
  - **不定义 `role` 字段**（固定创建 member，防提权）。
- 后端 `service.CreateUserByAdmin(ctx, tenantID, username, password)`：
  - 校验租户存在且启用（`ensureTenantActive`）;
  - 固定 `member` 角色落库；
  - 复用 `registerInTx` → 继承「用户名全局唯一」（需求单 0004 应用层全库查重）+ bcrypt 密码哈希。
- 成功返回 `{id, tenant_id, username, role}`，并写审计 `operation="创建员工"`。

**配套安全修复：公开注册强制 member**：

- `service.Register` 在租户校验后强制 `role = "member"`，**忽略/降级任何传入的 role**（包括 `admin`）。
- 合法建管理员流程不受影响：`RegisterTenant` 在建租户事务内直调 `registerInTx(... "admin")`（经 `tenant.go:79`），不经过 `Register`。

### 3.2 前端（管理后台 admin.html / admin.js）

在管理后台新增「员工管理」区块（置于「工具配置」之前）：

- 只有「用户名」+「密码」两个输入框 +「创建员工」按钮（**无租户选择**——租户由当前登录管理员身份决定）。
- 前端简单校验（用户名/密码非空、密码 ≥6 位），调 `api.post('/admin/user', { username, password })`。
- 显示成功/失败结果；用户名重复、参数错误等由后端返回并展示。
- 说明文案提示"用户名全局唯一、创建为普通成员（member）"。

## 四、涉及文件清单

| 文件 | 改动类型 |
|------|---------|
| `api/handler/admin_user.go` | **新增**：`AdminCreateUserReq` + `AdminCreateUser` handler（JWT 取 tenant、固定 member、审计）|
| `api/router.go` | `admin.POST("/user", handler.AdminCreateUser)`（挂 admin 组）|
| `api/service/user.go` | `Register` 强制 member（堵提权）；新增 `CreateUserByAdmin` |
| `api/service/account_test.go` | 更新 `TestRegister_ToExistingTenant`（传 admin 被强制 member）；新增 `TestCreateUserByAdmin` |
| `web/admin.html` | 新增「员工管理」section |
| `web/js/admin.js` | 新增 `createEmployee()` |

## 五、验证记录

- [x] `TestRegister_ToExistingTenant`：公开注册传 `role=admin` 被强制为 `member` ✅（提权口子已堵）。
- [x] `TestCreateUserByAdmin`：管理员创建员工固定 member / 不存在租户拒绝 / 用户名重复拒绝 ✅。
- [x] `go vet ./...` / `go test ./...` 全绿。
- [x] 独立 review：ship —— 提权堵死、admin 接口鉴权+租户隔离到位、复用 registerInTx、前端字段匹配。
- [x] 端到端（调用链）：`POST /api/admin/user` 无 token 返回 401（路由已注册、鉴权生效；旧代码 404）。

## 六、提交记录

- `3507a58` feat(用户): 普通用户由管理员创建——公开注册强制 member 堵提权；新增 admin 专属 `POST /api/admin/user` 创建员工；admin 界面加创建员工表单

## 七、范围外 / 后续可加

- **同租户多管理员**：当前仅建租户时产生首个 admin，本需求不提供"再建 admin"入口。DB 层 `idx_tenant_user` 不限制同租户 admin 数量，若需要可在后续给管理后台加"创建管理员"受控入口（仍 JWT 取租户、仅 admin 可调）。
- 员工列表/删除/重置密码：当前仅"创建"，未提供员工的列表、删除、改密码等管理（如需可后续加）。
- 公开自助注册普通用户：保持关闭（普通员工只能由管理员创建），如需开放可在后续单独设计邀请码等机制。
