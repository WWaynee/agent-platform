package service

// ============ Service 层：业务层 ============
//
// 负责：
//   1. 编写业务逻辑（如：创建租户时同时初始化默认工具配置）
//   2. 事务管理（多表操作保证一致性）
//   3. 可调用多个 storage 的数据操作方法
//   4. 业务规则、权限判断
//
// 不直接处理 HTTP 请求参数，由 handler 层传入。
// 不直接写原生 SQL，通过 storage 层访问数据库。

// TenantService 租户相关业务逻辑
// 占位文件，后续实现：
//   - CreateTenant  创建租户（+ 初始化默认工具配置）
//   - ListTenants   租户列表
//   - GetTenant     租户详情
//   - UpdateStatus  切换租户状态
