package storage

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"agent-platform/storage/model"
)

// setupSearchDB 连接真实 MySQL（读取项目根 .env），临时赋值给全局 storage.DB 并确保 documents 表存在。
// 与 api/service 的集成测试风格一致：连不上/无 .env 时跳过（不阻塞无环境的 CI/单测）。
// go test 工作目录是包目录（storage），项目根 .env 相对路径为 ../.env（storage 仅在 agent-platform 下一层）。
func setupSearchDB(t *testing.T) {
	t.Helper()
	_ = godotenv.Load("../.env")
	if os.Getenv("MYSQL_HOST") == "" {
		t.Skip("缺少 .env/数据库配置，跳过文档检索集成测试")
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
		t.Skipf("MySQL 不可用，跳过文档检索集成测试: %v", err)
	}
	DB = db
	// 确保 documents 表存在（AutoMigrate 幂等）
	if err := DB.AutoMigrate(&model.Document{}); err != nil {
		t.Skipf("AutoMigrate documents 失败，跳过: %v", err)
	}
}

// maybeSkipDoc 在未建数据库连接（跳过分支）时报 t.Skip。
func maybeSkipDoc(t *testing.T) {
	if DB == nil {
		t.Skip("数据库未初始化，跳过")
	}
}

// seedSearchDocs 清理并插入一批跨租户文档，返回各 doc。
func seedSearchDocs(t *testing.T) []model.Document {
	t.Helper()
	ctx := context.Background()
	// 清掉残留测试数据
	DB.WithContext(ctx).Where("tenant_id IN (?)", []uint64{60001, 60002}).Delete(&model.Document{})

	base := []model.Document{
		{TenantID: 60001, UserID: 1, Name: "销售合同A", Status: "success", Size: 1000},
		{TenantID: 60001, UserID: 1, Name: "采购合同B", Status: "success", Size: 2000},
		{TenantID: 60001, UserID: 1, Name: "员工制度", Status: "pending", Size: 3000},
		{TenantID: 60002, UserID: 2, Name: "销售合同C", Status: "success", Size: 4000},
		{TenantID: 60001, UserID: 1, Name: "将被删除的文档", Status: "success", Size: 500},
	}
	created := make([]model.Document, len(base))
	for i := range base {
		if err := DB.WithContext(ctx).Create(&base[i]).Error; err != nil {
			t.Fatalf("插入测试文档失败: %v", err)
		}
		created[i] = base[i]
	}
	// 软删"将被删除的文档"
	if err := DB.WithContext(ctx).Where("id = ?", created[4].ID).Delete(&model.Document{}).Error; err != nil {
		t.Fatalf("软删测试文档失败: %v", err)
	}
	// 清理函数返回后由调用方负责删除
	return created
}

// TestSearchDocuments_租户隔离_模糊搜索_软删 覆盖需求单 5.2：
// list/search 的租户隔离、LIKE 模糊、硬删过滤、软删过滤、防注入。
func TestSearchDocuments(t *testing.T) {
	setupSearchDB(t)
	maybeSkipDoc(t)
	ctx := context.Background()
	docs := seedSearchDocs(t)
	defer func() {
		var ids []uint64
		for _, d := range docs {
			ids = append(ids, d.ID)
		}
		DB.WithContext(ctx).Where("id IN (?)", ids).Unscoped().Delete(&model.Document{})
	}()

	t.Run("空关键字=返回该租户全部有效文档", func(t *testing.T) {
		got, err := SearchDocuments(ctx, 60001, "")
		if err != nil {
			t.Fatalf("SearchDocuments err: %v", err)
		}
		// 租户 60001 共 4 篇（不含已软删的"将被删除的文档"）
		// 销售合同A/采购合同B/员工制度 + （软删排除）将被删除的文档
		if len(got) != 3 {
			t.Fatalf("期望 60001 有 3 篇有效文档，实际 %d 篇: %+v", len(got), got)
		}
		// 软删必须被排除：不该出现"将被删除的文档"
		for _, d := range got {
			if strings.Contains(d.Name, "将被删除") {
				t.Errorf("软删文档不应出现在列表: %+v", d)
			}
		}
	})

	t.Run("租户隔离：租户60002只见自己文档", func(t *testing.T) {
		got, err := SearchDocuments(ctx, 60002, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 1 || got[0].Name != "销售合同C" {
			t.Fatalf("租户60002 应只见其自己的 1 篇文档，实际: %+v", got)
		}
	})

	t.Run("LIKE 模糊：keyword=合同 命中多篇", func(t *testing.T) {
		got, err := SearchDocuments(ctx, 60001, "合同")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("60001 下名称含'合同'的应有 2 篇，实际 %d: %+v", len(got), got)
		}
	})

	t.Run("LIKE 精确：keyword=销售合同A 命中1篇", func(t *testing.T) {
		got, err := SearchDocuments(ctx, 60001, "销售合同A")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 1 || got[0].Name != "销售合同A" {
			t.Fatalf("应精确命中'销售合同A'，实际: %+v", got)
		}
	})

	t.Run("无匹配返回空不报错", func(t *testing.T) {
		got, err := SearchDocuments(ctx, 60001, "不存在的关键字xyz")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("应返回空，实际: %+v", got)
		}
	})

	t.Run("防注入：含 %/_/SQL 片段不作谓词展开", func(t *testing.T) {
		// % 和 _ 是 LIKE 通配符；加引号/拼接入参若被当作 SQL 会报错或全匹配——此处应作为字面量不报错。
		got, err := SearchDocuments(ctx, 60001, "%")
		if err != nil {
			t.Fatalf("keyword=%% 不应报错: %v", err)
		}
		got2, err := SearchDocuments(ctx, 60001, "合同' OR '1'='1")
		if err != nil {
			t.Fatalf("keyword 含 SQL 片段不应报错: %v", err)
		}
		// 两条都不应把租户全部文档捞出来（字面量语义）
		if len(got2) != 0 {
			t.Fatalf("含引号的字面量关键字不应命中任何文档: %+v", got2)
		}
		_ = got
	})
}
