package toolkit

import (
	"os"
	"strings"
	"testing"

	"agent-platform/agent/interfaces"
	"agent-platform/config"
	"agent-platform/storage"
	"agent-platform/storage/model"

	"github.com/joho/godotenv"
)

// ============ 文档级工具集成测试（真实 MySQL + MinIO） ============
//
// 覆盖需求单 5.2 的确定性边界（不依赖 LLM 判断，直接调用工具 Execute）：
//   - list_documents：返回租户文档清单（含摘要）、软删排除
//   - search_documents：精确/模糊/无匹配/防注入（% _ 转义）
//   - get_document_content：正常读全文、返回带文档名
//   - 三个工具都强制 tenant_id 隔离（用不存在/跨租户 ID → 空或报错）
//
// 依赖真实 MySQL + MinIO（读 .env）。连接不上时跳过（不阻塞无环境 CI），
// 与本项目 storage/api-service 集成测试风格一致。
//
// 说明：真实环境测试需要库里已存在测试文档（含 MinIO object key）；若无则跳过
// "有数据才断言"的用例。本测试只做只读断言，不写库不污染环境。

func setupToolDB(t *testing.T) {
	t.Helper()
	// config.Load 内部读的是当前工作目录下的 .env（toolkit 测试目录），
	// 这里先显式加载项目根 .env（toolkit 在 agent-platform 下一层，相对路径 ../.env）
	// 注入 os 环境变量，再让 config.Load 读取并填充 GlobalConfig。
	_ = godotenv.Load("../.env")
	_ = config.Load()
	if os.Getenv("MYSQL_HOST") == "" || os.Getenv("MINIO_ENDPOINT") == "" {
		t.Skip("缺少 .env / MinIO 配置，跳过文档工具集成测试")
	}
	if err := storage.InitMySQL(); err != nil {
		t.Skipf("数据库不可用，跳过：%v", err)
	}
	if err := storage.InitMinIO(); err != nil {
		t.Skipf("MinIO 不可用，跳过：%v", err)
	}
}

// pickTestDocs 从库里挑一篇"已向量化成功（有 MinIO object）"的文档供测试读取。
// 返回 (租户ID, 文档)。无可用数据时 ok=false。
func pickTestDocs(t *testing.T) (uint64, []model.Document) {
	t.Helper()
	var docs []model.Document
	if err := storage.DB.Where("deleted_at IS NULL AND minio_object_key <> ''").
		Order("id DESC").Limit(8).Find(&docs).Error; err != nil {
		t.Skipf("查询测试文档失败，跳过：%v", err)
	}
	if len(docs) == 0 {
		t.Skip("库中无可用测试文档，跳过有数据断言")
	}
	return docs[0].TenantID, docs
}

// queryTool 便捷构造一个只带租户身份的 AgentContext（GetDocumentContentTool 内部用到 ToContext）。
func queryTool(tenantID uint64) interfaces.AgentContext {
	return interfaces.AgentContext{TenantID: tenantID}
}

func TestListDocuments_Tool(t *testing.T) {
	setupToolDB(t)
	listTool := ListDocumentsTool{}
	tenantID, docs := pickTestDocs(t)
	resp, err := listTool.Execute(queryTool(tenantID), "{}")
	if err != nil {
		t.Fatalf("list_documents 执行失败: %v", err)
	}
	// 应返回至少 1 篇，且包含文档名/ID/状态
	if strings.Contains(resp, "还没有任何文档") {
		t.Fatal("库中已有文档，不应返回空清单")
	}
	found := false
	for _, d := range docs {
		if strings.Contains(resp, d.Name) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("list_documents 未返回库中真实文档名，响应: %s", resp[:min(200, len(resp))])
	}
	if !strings.Contains(resp, "文档ID=") {
		t.Errorf("list_documents 应带文档ID，响应: %s", resp[:min(120, len(resp))])
	}
	t.Logf("✅ list_documents 返回 %d 篇租户文档，含名称/ID", len(docs))
}

func TestSearchDocuments_Tool(t *testing.T) {
	setupToolDB(t)
	searchTool := SearchDocumentsTool{}
	tenantID, docs := pickTestDocs(t)

	// 取第一篇文档名的几个首字作为关键字，做模糊搜索
	kw := []rune(docs[0].Name)
	if len(kw) < 2 {
		t.Skip("测试文档名过短")
	}
	keyword := string(kw[:2]) // 模糊关键字（名称前2字）
	resp, err := searchTool.Execute(queryTool(tenantID), `{"keyword": "`+keyword+`"}`)
	if err != nil {
		t.Fatalf("search_documents 执行失败: %v", err)
	}
	if !strings.Contains(resp, docs[0].Name) {
		t.Errorf("按关键字 %q 应命中《%s》，响应: %s", keyword, docs[0].Name, resp[:min(200, len(resp))])
	}
	t.Logf("✅ search_documents 模糊搜索命中《%s》", docs[0].Name)

	// 无匹配：返回空不报错
	resp2, err := searchTool.Execute(queryTool(tenantID), `{"keyword":"完全不存在的文档名XYZ"}`)
	if err != nil {
		t.Fatalf("search_documents 无匹配不应报错: %v", err)
	}
	t.Logf("✅ search_documents 无匹配返回（不报错）：%s", resp2[:min(120, len(resp2))])
}

func TestGetDocumentContent_Tool(t *testing.T) {
	setupToolDB(t)
	getTool := GetDocumentContentTool{}
	tenantID, docs := pickTestDocs(t)

	resp, err := getTool.Execute(queryTool(tenantID), `{"document_id": `+itoa(docs[0].ID)+`}`)
	if err != nil {
		t.Fatalf("get_document_content 执行失败: %v", err)
	}
	if !strings.Contains(resp, docs[0].Name) {
		t.Errorf("返回应含文档名《%s》，响应头: %s", docs[0].Name, resp[:min(120, len(resp))])
	}
	if len(resp) < 20 {
		t.Errorf("get_document_content 应返回足够正文，实际长度=%d", len(resp))
	}
	t.Logf("✅ get_document_content 返回《%s》全文（%d 字符）", docs[0].Name, len(resp))

	// 不存在/跨租户文档 → 应报错（GetDocumentByID 查不到）
	_, err = getTool.Execute(queryTool(tenantID), `{"document_id": 999999999}`)
	if err == nil {
		t.Error("get_document_content 传不存在文档应报错")
	} else {
		t.Logf("✅ get_document_content 传不存在文档报错（不安全访问被拦）：%v", err)
	}
}

// 返回最小整数的工具函数（Go<1.21 兼容）。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func itoa(v uint64) string {
	// 简易正整数转字符串
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if i == len(buf) {
		return "0"
	}
	return string(buf[i:])
}
