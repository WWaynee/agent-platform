package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"agent-platform/agent/interfaces"
	"agent-platform/storage"
	"agent-platform/storage/model"
	"agent-platform/util"
)

// ============ Service 层：用户业务逻辑 ============

// Register 用户注册
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 DB 日志带同一链路 ID。
//
// 流程：① 校验租户存在 → ② 校验用户名是否已存在 → ③ 密码 bcrypt 哈希 → ④ 插入数据库 → 返回用户
//
// 角色规则：注册接口创建的是普通成员 member。⚠️ 公开注册**强制 member**，忽略调用方传入的
// role（即使传 "admin" 也只落 member）——堵住「任意人凭 tenant_id 就能给该租户建管理员」的提权口子
// （提权只允许走管理员专属接口 /api/admin/user，或建租户时系统自动生成首个 admin）。
//
// ⚠️ 多租户安全（对应需求单 0001）：注册是公开接口、只能靠前端自报 tenant_id，
// 是全链路唯一信任前端自报 tenant_id 的口子。故必须校验该租户**存在**，
// 否则匿名用户可凭空编造不存在的租户 ID 造出"无主租户孤儿账号"，绕过建租户流程。
func Register(ctx context.Context, tenantID uint64, username, password, role string) (*model.User, error) {
	// 0. 校验租户存在（堵"孤儿账号"）。
	if err := ensureTenantActive(ctx, tenantID); err != nil {
		return nil, err
	}

	// 公开注册一律创建普通成员 member：忽略调用方 role 参数，强制 member，
	// 防止匿名提权为 admin（管理员账号只能由管理员专属接口或建租户流程产生）。
	role = "member"

	// 用默认连接执行注册（非事务场景：普通自助注册）。
	return registerInTx(ctx, storage.DB, tenantID, username, password, role)
}

// CreateUserByAdmin 管理员为当前租户创建普通员工（member）。
// ⚠️ 仅供管理员专属接口 /api/admin/user 调用：tenantID 由调用方（handler）从 JWT 取（即管理员所属租户），
// 不相信前端 body 传入的租户。创建的用户**固定 member**（不允许管理员在此提权成 admin，管理员仍由
// 建租户流程或额外超管接口产生）。
func CreateUserByAdmin(ctx context.Context, tenantID uint64, username, password string) (*model.User, error) {
	// 校验租户存在且启用
	if err := ensureTenantActive(ctx, tenantID); err != nil {
		return nil, err
	}
	// 固定创建普通成员 member
	return registerInTx(ctx, storage.DB, tenantID, username, password, "member")
}

// registerInTx 在给定的 *gorm.DB（默认连接或事务句柄）内完成用户注册的核心逻辑：
//
//	查重用户名 → 密码哈希 → 创建用户。
//
// 抽出该内部函数供两处复用：
//   - Register：用默认连接 storage.DB 执行（普通自助注册）；
//   - RegisterTenant：在建租户事务内用事务句柄 tx 执行（首个 admin，与其它子步骤原子提交）。
func registerInTx(ctx context.Context, db *gorm.DB, tenantID uint64, username, password, role string) (*model.User, error) {
	// 1. 校验「用户名」是否已全局存在（需求：用户名必须全局唯一，不允许跨租户同名）。
	//    从"本租户同名"收紧为"全库同名"——任一租户已注册该用户名则拒绝，前端据此提示"不允许通过"。
	var count int64
	if err := db.WithContext(ctx).Model(&model.User{}).
		Where("username = ?", username).
		Count(&count).Error; err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	if count > 0 {
		return nil, fmt.Errorf("用户名已存在，请更换")
	}

	// 2. 密码哈希
	passwordHash, err := util.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("密码加密失败: %w", err)
	}

	// 3. 角色兜底：未显式指定则默认普通成员
	if role == "" {
		role = "member"
	}

	// 4. 构造用户并插入（用传入的 db 句柄，支持默认连接与事务句柄）
	user := &model.User{
		TenantID:     tenantID,
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		Status:       1, // 默认启用
	}
	if err := storage.CreateUserTx(ctx, db, user); err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	return user, nil
}

// ensureTenantActive 校验指定租户存在且启用；租户不存在或已禁用则返回明确错误。
// 用于注册 / 登录（堵问题：孤儿账号 / 禁用租户内账号仍可登录）。
func ensureTenantActive(ctx context.Context, tenantID uint64) error {
	tenant, err := storage.GetTenantByID(ctx, tenantID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("租户/公司不存在")
		}
		return fmt.Errorf("查询租户失败: %w", err)
	}
	if tenant.Status != 1 {
		return fmt.Errorf("该租户已被禁用，请联系管理员")
	}
	return nil
}

// Login 用户登录
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 DB 日志带同一链路 ID。
//
// 登录方式（需求单 0004 起支持两种）：
//   - tenantID > 0：按「租户 + 用户名」登录（兼容老调用方 / e2e 脚本）；
//   - tenantID == 0：按「用户名」全局登录（前端只输用户名+密码）——
//     用户名全库唯一则直接登录；存在多个租户同名则返回明确错误（无法消除歧义）；
//     不存在则统一「用户名或密码错误」。
//
// 无论用户不存在还是密码错误，统一返回"用户名或密码错误"（安全考虑，不让攻击者探测用户名是否有效）
//
// ⚠️ 多租户安全（对应需求单 0001）：登录是公开接口、只能靠前端自报 tenant_id，
// 故登录时必须校验用户所属租户**存在且启用**：
//   - 租户不存在（孤儿账号）→ 拒绝登录；
//   - 租户被禁用 → 拒绝登录（禁用租户内的账号不能继续登录）。
func Login(ctx context.Context, tenantID uint64, username, password string) (*model.User, error) {
	// 1. 定位用户：优先按「租户+用户名」精确查（老方式）；tenantID==0 时按用户名全局解析唯一身份
	var user *model.User
	if tenantID > 0 {
		u, err := storage.GetUserByUsername(ctx, tenantID, username)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("用户名或密码错误")
			}
			return nil, fmt.Errorf("查询用户失败: %w", err)
		}
		user = u
	} else {
		u, err := loginResolveByUsername(ctx, username)
		if err != nil {
			return nil, err
		}
		user = u
	}

	// 2. 校验租户存在且启用（堵孤儿账号 / 禁用租户登录）
	if err := ensureTenantActive(ctx, user.TenantID); err != nil {
		return nil, err
	}

	// 3. 校验密码
	if !util.VerifyPassword(password, user.PasswordHash) {
		return nil, fmt.Errorf("用户名或密码错误")
	}

	// 4. 密码正确，返回用户
	// 审计：登录是公开接口，登录前 ctx 还没有 user_id（未鉴权），故用查到的 user 补齐租户/用户再记录。
	RecordAuditLog(interfaces.WithTenantUser(ctx, user.TenantID, user.ID), "登录", fmt.Sprintf("用户 %q 登录成功", user.Username))
	return user, nil
}

// loginResolveByUsername 按用户名全库解析唯一登录身份（需求单 0004）。
//   - 命中 0 个 → 统一「用户名或密码错误」（不区分存在性，防探测）；
//   - 命中 1 个 → 返回该用户；
//   - 命中多个（不同租户共存同名）→ 返回明确错误，引导用户提供租户标识。
func loginResolveByUsername(ctx context.Context, username string) (*model.User, error) {
	users, err := storage.FindUsersByUsername(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	switch len(users) {
	case 0:
		return nil, fmt.Errorf("用户名或密码错误")
	case 1:
		return &users[0], nil
	default:
		return nil, fmt.Errorf("该用户名在多个租户下存在，无法自动登录，请提供租户 ID")
	}
}
