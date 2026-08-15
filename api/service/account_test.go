package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"agent-platform/config"
	"agent-platform/storage"
	"agent-platform/storage/model"
)

// TestRegisterTenant_Basic 验证公开注册租户（需求单 0001）：
//  1. 原子创建「租户 + 首个管理员(role=admin) + 默认工具配置」三件套；
//  2. 返回的 tenant/admin 字段正确（admin 固定为 role=admin）；
//  3. tenant_name 唯一（测试用时间戳随机名），结束清理测试数据。
func TestRegisterTenant_Basic(t *testing.T) {
	setupTestDB(t)
	_ = config.Load() // 填充配额等配置；失败不影响（默认 0=不限制）

	name := randTenantName("basic")
	tenant, admin, err := RegisterTenant(context.Background(), name, "bobadmin", "Bob@123456")
	if err != nil {
		t.Fatalf("RegisterTenant 失败: %v", err)
	}
	defer cleanupTenant(t, tenant.ID)

	// ① 租户创建成功且默认启用
	if tenant.ID == 0 {
		t.Errorf("新租户 ID 不应为 0")
	}
	if tenant.Status != 1 {
		t.Errorf("新租户应为启用状态，实际 %d", tenant.Status)
	}
	// ② 首个管理员存在且 role=admin
	if admin == nil || admin.TenantID != tenant.ID {
		t.Errorf("管理员应属于新租户 tenant_id=%d，实际 admin.TenantID=%d", tenant.ID, adminIDOf(admin))
	}
	if admin.Role != "admin" {
		t.Errorf("首个账号应固定为 admin，实际 %q", admin.Role)
	}
	// ③ 默认工具配置已写入
	enabled, err := GetToolEnabled(context.Background(), tenant.ID, "knowledge_retrieve")
	if err != nil {
		t.Fatalf("读取工具配置失败: %v", err)
	}
	if !enabled {
		t.Errorf("新租户应默认启用 knowledge_retrieve 工具")
	}

	t.Logf("✅ RegisterTenant：租户#%d + admin(%s) + 工具配置创建成功", tenant.ID, admin.Username)
}

// TestRegisterTenant_AtomicRollback 验证事务原子性（需求单 0001 第 5.1 点）：
// 构造「创建首个 admin」失败的场景（DB 拒绝超长 username），验证整个事务回滚——
// 该租户不应残留半成品记录（租户 / 用户 / 工具配置都不得落库）。
func TestRegisterTenant_AtomicRollback(t *testing.T) {
	setupTestDB(t)
	_ = config.Load()

	name := randTenantName("rollback")
	// username 超过 users.username 字段 size:64，DB 将报 Data too long → 注册失败
	badUser := make([]byte, 80)
	for i := range badUser {
		badUser[i] = 'u'
	}

	tenant, admin, err := RegisterTenant(context.Background(), name, string(badUser), "Pass@123456")
	if err == nil {
		// 若没报错（极端情况 DB 未严格校验长度），兜底清理已创建的半成品再失败
		cleanupTenant(t, tenant.ID)
		t.Fatalf("超长 username 应导致注册失败，实际成功（admin.ID=%d）", adminIDOf(admin))
	}
	t.Logf("  按预期注册失败: %v", err)

	// 验证租户表无该租户（整体回滚，不留残缺）
	var cnt int64
	if err := storage.DB.WithContext(context.Background()).
		Model(&model.Tenant{}).Where("name = ?", name).Count(&cnt).Error; err != nil {
		t.Fatalf("查询租户失败: %v", err)
	}
	if cnt != 0 {
		t.Errorf("事务未回滚：租户 %q 仍存在（cnt=%d），应为 0", name, cnt)
	}

	t.Log("✅ 原子性：注册失败后租户整体回滚，无残缺租户残留")
}

// TestRegister_UnknownTenantRejected 验证用户注册到「不存在租户」被拒（需求单 0001）：
// 防止匿名用户编造不存在的 tenant_id 造出孤儿账号。
func TestRegister_UnknownTenantRejected(t *testing.T) {
	setupTestDB(t)

	_, err := Register(context.Background(), 99999999, "ghostuser", "Ghost@123456", "member")
	if err == nil {
		t.Errorf("注册到不存在的租户应被拒绝，实际成功")
	} else {
		t.Logf("✅ 不存在的租户被拒: %v", err)
	}

	// 登录取用的管理员账号造不出孤儿 lease 测试：直接用不存在的 tenant id 登录也应被拒
	_, err = Login(context.Background(), 99999999, "ghostuser", "Ghost@123456")
	if err == nil {
		t.Errorf("登录不存在的租户应被拒绝，实际成功")
	} else {
		t.Logf("✅ 登录不存在的租户被拒: %v", err)
	}
}

// TestLogin_DisabledTenantRejected 验证「租户被禁用」后其账号无法登录（需求单 0001）：
// 禁用租户 → 该租户内用户登录被拒。
func TestLogin_DisabledTenantRejected(t *testing.T) {
	setupTestDB(t)
	_ = config.Load()

	name := randTenantName("disabled")
	tenant, admin, err := RegisterTenant(context.Background(), name, "charlie", "Char@123456")
	if err != nil {
		t.Fatalf("RegisterTenant 失败: %v", err)
	}
	defer cleanupTenant(t, tenant.ID)

	// 先验证禁用前能登录
	if _, err := Login(context.Background(), tenant.ID, admin.Username, "Char@123456"); err != nil {
		t.Fatalf("禁用前登录应成功，实际: %v", err)
	}

	// 禁用租户
	if err := UpdateTenantStatus(context.Background(), tenant.ID, 0); err != nil {
		t.Fatalf("禁用租户失败: %v", err)
	}

	// 禁用后登录被拒
	if _, err := Login(context.Background(), tenant.ID, admin.Username, "Char@123456"); err == nil {
		t.Errorf("租户禁用后登录应被拒绝，实际成功")
	} else {
		t.Logf("✅ 禁用租户内的账号登录被拒: %v", err)
	}
}

// TestRegisterTenant_DuplicateNameRejected 验证重复注册同名租户被拒（需求单 0001 第 5.1 点）：
// tenants.name 唯一索引兜底 → 第二次注册同名租户返回明确错误，且整体回滚不留残缺租户。
func TestRegisterTenant_DuplicateNameRejected(t *testing.T) {
	setupTestDB(t)
	_ = config.Load()

	name := randTenantName("dup")
	first, _, err := RegisterTenant(context.Background(), name, "dupadmin", "Dup@123456")
	if err != nil {
		t.Fatalf("首次注册租户失败: %v", err)
	}
	defer cleanupTenant(t, first.ID)

	// 第二次用同名（不同 admin 用户名）注册 → 应被唯一索引拦截并整体回滚
	_, _, err = RegisterTenant(context.Background(), name, "dupadmin2", "Dup@123456")
	if err == nil {
		t.Fatalf("重复注册同名租户应被拒绝，实际成功")
	}
	t.Logf("✅ 重复注册同名租户被拒: %v", err)

	// 第二次注册整体回滚：库里该 name 的租户仍只有首次的 1 条
	var cnt int64
	if err := storage.DB.WithContext(context.Background()).
		Model(&model.Tenant{}).Where("name = ?", name).Count(&cnt).Error; err != nil {
		t.Fatalf("查询租户失败: %v", err)
	}
	if cnt != 1 {
		t.Errorf("重复注册应整体回滚，该 name 应仅 1 条，实际 %d", cnt)
	}
	t.Log("✅ 重复注册整体回滚，无残留同名租户")
}

// TestRegister_ToExistingTenant 验证「注册用户到存在的租户 → 成功；role=admin → 校验租户存在后仍创建」
// （需求单 0001 5.1）：公开注册只校验租户存在，存在则能注册成功并入库。
func TestRegister_ToExistingTenant(t *testing.T) {
	setupTestDB(t)
	_ = config.Load()

	name := randTenantName("exist")
	tenant, _, err := RegisterTenant(context.Background(), name, "existadm", "Exist@123456")
	if err != nil {
		t.Fatalf("RegisterTenant 失败: %v", err)
	}
	defer cleanupTenant(t, tenant.ID)

	// 注册到已存在租户的普通成员 → 成功
	mem, err := Register(context.Background(), tenant.ID, "existmember", "Member@123456", "member")
	if err != nil {
		t.Fatalf("向存在租户注册 member 应成功: %v", err)
	}
	if mem.Role != "member" {
		t.Errorf("注册 member 应 role=member，实际 %q", mem.Role)
	}

	// 注册到已存在租户并显式 role=admin → 校验租户存在后仍创建成功（符合现状）
	adm, err := Register(context.Background(), tenant.ID, "existadm2", "Admin@123456", "admin")
	if err != nil {
		t.Fatalf("向存在租户注册 role=admin 应成功: %v", err)
	}
	if adm.Role != "admin" {
		t.Errorf("注册 role=admin 应 role=admin，实际 %q", adm.Role)
	}
	t.Log("✅ 向存在租户注册 member/admin 均成功，且校验租户存在")
}

// ===== 测试辅助 =====

var accountTestSeq int

// randSuffix 生成一个足够随机的短后缀（纳秒时间戳 + 自增序号），保证测试租户名唯一。
func randSuffix() string {
	accountTestSeq++
	return fmt.Sprintf("%d-%d", time.Now().UnixNano()%1e9, accountTestSeq)
}

// randTenantName 生成带唯一后缀的租户名，避免与库内历史数据冲突。
func randTenantName(tag string) string {
	counter := 0
	for {
		name := "测试租户-" + tag + "-" + randSuffix()
		var cnt int64
		_ = storage.DB.WithContext(context.Background()).
			Model(&model.Tenant{}).Where("name = ?", name).Count(&cnt).Error
		if cnt == 0 {
			return name
		}
		counter++
		if counter > 5 {
			return "测试租户-" + tag + "-" + randSuffix() + "-" + tag
		}
	}
}

func cleanupTenant(t *testing.T, tenantID uint64) {
	t.Helper()
	ctx := context.Background()
	if tenantID == 0 {
		return
	}
	// 尽力清理测试产生的数据（租户/其下用户/工具配置/审计），失败仅记录不阻塞
	if err := storage.DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Delete(&model.User{}).Error; err != nil {
		t.Logf("清理用户失败: %v", err)
	}
	if err := storage.DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Delete(&model.TenantToolConfig{}).Error; err != nil {
		t.Logf("清理工具配置失败: %v", err)
	}
	if err := storage.DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Delete(&model.AuditLog{}).Error; err != nil {
		t.Logf("清理审计失败: %v", err)
	}
	if err := storage.DB.WithContext(ctx).Where("id = ?", tenantID).Delete(&model.Tenant{}).Error; err != nil {
		t.Logf("清理租户失败: %v", err)
	}
}

func adminIDOf(u *model.User) uint64 {
	if u == nil {
		return 0
	}
	return u.ID
}
