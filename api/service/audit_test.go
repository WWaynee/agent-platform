package service

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"agent-platform/agent/interfaces"
	"agent-platform/storage"
	"agent-platform/storage/model"
)

// setupTestDB 连接真实 MySQL（读取项目根 .env），临时赋值给全局 storage.DB。
// 审计日志需真实落库验证，故连真实库做集成测试；连不上/无 .env 时跳过（不阻塞其他单测）。
func setupTestDB(t *testing.T) {
	t.Helper()
	// 注意：不做 defer 还原——storage.DB 要为整个测试过程保持已设置状态。
	// 每次测试会重新赋值，测试进程结束后自然回收；其它测试（如 health_test）各自管理 DB 状态。
	// go test 工作目录是包目录（api/service），项目根 .env 相对路径为 ../../.env。
	_ = godotenv.Load("../../.env")
	if os.Getenv("MYSQL_HOST") == "" {
		t.Log("无 .env/数据库配置，跳过审计日志集成测试")
		t.Skip("缺少 .env 配置")
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		os.Getenv("MYSQL_USER"),
		os.Getenv("MYSQL_ROOT_PWD"),
		os.Getenv("MYSQL_HOST"),
		os.Getenv("MYSQL_PORT"),
		os.Getenv("MYSQL_DB"),
	)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Skipf("MySQL 不可用，跳过审计日志集成测试: %v", err)
	}
	storage.DB = db
}

// TestRecordAuditLog 验证审计日志工具函数：
//  1. 从 ctx 提取 tenant_id / user_id / trace_id 写入 audit_logs；
//  2. operation / content 正确落库；
//  3. ctx 缺字段时安全留空、不报错（审计"尽力而为"）。
func TestRecordAuditLog(t *testing.T) {
	setupTestDB(t)

	// 构造带租户/用户/trace 的 ctx（模拟已登录请求上下文）
	ctx := interfaces.WithTenantUser(context.Background(), 4242, 777)
	ctx = interfaces.WithTraceID(ctx, "audit-trace-test-001")

	const op = "单元测试-上传文档"
	_ = storage.DB.WithContext(ctx).Where("operation = ?", op).Delete(&model.AuditLog{}).Error

	// 调用工具函数
	RecordAuditLog(ctx, op, "审计单测：上传示例文档.docx")

	// 查回验证
	var row model.AuditLog
	if err := storage.DB.WithContext(ctx).
		Where("operation = ?", op).
		Order("id DESC").
		First(&row).Error; err != nil {
		t.Fatalf("读取审计日志失败: %v", err)
	}
	defer storage.DB.WithContext(ctx).Delete(&row)

	if row.TenantID != 4242 {
		t.Errorf("tenant_id 应为 4242，实际 %d", row.TenantID)
	}
	if row.UserID != 777 {
		t.Errorf("user_id 应为 777，实际 %d", row.UserID)
	}
	if row.TraceID != "audit-trace-test-001" {
		t.Errorf("trace_id 应为 audit-trace-test-001，实际 %q", row.TraceID)
	}
	if row.Operation != op {
		t.Errorf("operation 应为 %q，实际 %q", op, row.Operation)
	}
	if row.Content == "" {
		t.Errorf("content 不应为空")
	}

	// ctx 缺字段时应安全写入（tenant/user/trace 留 0/空），不报错
	RecordAuditLog(context.Background(), "单元测试-无上下文", "空 ctx 也应能记录")
	var emptyRow model.AuditLog
	if err := storage.DB.WithContext(context.Background()).
		Where("operation = ?", "单元测试-无上下文").
		Order("id DESC").
		First(&emptyRow).Error; err != nil {
		t.Fatalf("空 ctx 写审计应成功，实际失败: %v", err)
	}
	defer storage.DB.WithContext(context.Background()).Delete(&emptyRow)

	t.Log("✅ RecordAuditLog：从 ctx 正确提取 tenant_id/user_id/trace_id 并落库；空 ctx 安全不报错")
}
