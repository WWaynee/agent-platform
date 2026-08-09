package storage

import (
	"context"
	"fmt"

	"github.com/qdrant/go-client/qdrant"

	"agent-platform/config"
)

// ============ Qdrant 向量检索封装 ============
//
// 作用：把 Qdrant（向量数据库）的写入/检索操作封装成简单的业务方法，
// 供 service/agent 层调用。屏蔽 gRPC SDK 细节，未来换向量库只改本层。
//
// 说明：
//   - 写入点(Point)时，payload 附带元数据（如 document_id、chunk_id、text），
//     检索命中后可据此回关联键找到原始文档/切片内容。
//   - 集合在初始化时自动创建（若不存在），向量维度由外部传入（由 Embedding 模型决定）。
//
// 注意：Qdrant 的 Client 内部用 gRPC 连接，多个方法并发调用安全（SDK 自带连接池）。

// QdrantClient 全局 Qdrant 客户端，业务代码直接使用
var QdrantClient *qdrant.Client

// DefaultVectorSize 向量默认维度。
// ⚠️ 必须与 Embedding 模型产出维度一致，否则写入 Qdrant 会因维度不匹配失败。
// 本项目实际 Embedding 模型为 Qwen/Qwen3-VL-Embedding-8B（硅基流动），
// 已实测其输出向量维度 = 4096。若更换 Embedding 模型，需同步修改此值。
const DefaultVectorSize = uint64(4096)

// InitQdrant 从 config 初始化 Qdrant 客户端，并确保目标集合存在。
// vectorSize: 向量维度（与 Embedding 模型产出的向量维度一致，如 1024）。
// 若集合不存在则按 Cosine 距离创建。
func InitQdrant(vectorSize uint64) error {
	cfg := config.GlobalConfig.Qdrant

	client, err := qdrant.NewClient(&qdrant.Config{
		Host: cfg.Host,
		// SDK 走 gRPC，用 gRPC 端口（Qdrant gRPC=6334，REST=6333）
		Port: cfg.GRPCPort,
		// 跳过客户端-服务端版本兼容检查（本地版本差异可能造成无谓告警）
		SkipCompatibilityCheck: true,
	})
	if err != nil {
		return fmt.Errorf("初始化 Qdrant 客户端失败: %w", err)
	}

	// 赋值给全局变量
	QdrantClient = client

	// 确保集合存在
	if err := ensureCollection(context.Background(), cfg.CollectionName, vectorSize); err != nil {
		return err
	}

	fmt.Println("[storage] Qdrant 连接成功")
	return nil
}

// ensureCollection 确保集合存在；不存在则创建。
// 若已存在则跳过（避免重复建集合报错）。
func ensureCollection(ctx context.Context, name string, vectorSize uint64) error {
	exists, err := QdrantClient.CollectionExists(ctx, name)
	if err != nil {
		return fmt.Errorf("检查集合是否存在失败: %w", err)
	}
	if exists {
		return nil
	}

	// 创建集合：向量维度 vectorSize，使用 Cosine 余弦相似度
	// （RAG 语义检索常用 Cosine，归一化后等同于内积）
	err = QdrantClient.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     vectorSize,
			Distance: qdrant.Distance_Cosine,
		}),
	})
	if err != nil {
		return fmt.Errorf("创建向量集合失败: %w", err)
	}
	fmt.Printf("[storage] 已自动创建向量集合: %s (维度=%d, 距离=Cosine)\n", name, vectorSize)
	return nil
}

// ============ 对外封装：写入 ============

// QdrantVector 要写入向量库的一条向量及其必填元数据。
// ⚠️ 多租户隔离核心：每一条都必须带上 tenant_id，否则检索时无法按租户隔离，
// 且 tenant_id 是检索过滤键，缺失会导致数据能被他租户检索到（安全事故）。
// Content 直接落 payload，检索时随结果返回，无需再回查数据库。
type QdrantVector struct {
	ID         uint64    // 点全局唯一 ID（建议用 document_id<<32 | chunk_index 合成，防跨文档冲突）
	TenantID   uint64    // 租户 ID（检索过滤键）
	DocumentID uint64    // 文档 ID（归属的文档）
	ChunkIndex int       // 第几个切片（从 0 开始）
	Content    string    // 切片原文内容（检索结果直接携带）
	Vector     []float32 // 向量（如 Qwen/Qwen3-VL-Embedding-8B = 4096 维）
}

// toPointStruct 把业务层的 QdrantVector 转成 Qdrant 的 PointStruct（含 payload）。
// payload 蕴含 tenant_id/document_id/chunk_index/content 四个必填元数据。
func toPointStruct(v QdrantVector) (*qdrant.PointStruct, error) {
	payload := map[string]*qdrant.Value{}
	for key, val := range map[string]int64{
		"tenant_id":   int64(v.TenantID),
		"document_id": int64(v.DocumentID),
		"chunk_index": int64(v.ChunkIndex),
	} {
		v64, err := qdrant.NewValue(val)
		if err != nil {
			return nil, fmt.Errorf("构造 payload 字段 %s 失败: %w", key, err)
		}
		payload[key] = v64
	}
	contentVal, err := qdrant.NewValue(v.Content)
	if err != nil {
		return nil, fmt.Errorf("构造 payload 字段 content 失败: %w", err)
	}
	payload["content"] = contentVal

	return &qdrant.PointStruct{
		Id:      qdrant.NewIDNum(v.ID),
		Vectors: qdrant.NewVectors(v.Vector...),
		Payload: payload,
	}, nil
}

// UpsertVectors 批量写入向量到集合。
// items: 多个待写入的向量及其元数据（每个必须在 QdrantVector 里带齐
//
//	tenant_id/document_id/chunk_index/content）。
//
// 一次性批量 upsert，减少与 Qdrant 的往返次数；同 ID 重复写入会覆盖。
func UpsertVectors(ctx context.Context, items []QdrantVector) error {
	if len(items) == 0 {
		return nil
	}
	collection := config.GlobalConfig.Qdrant.CollectionName

	points := make([]*qdrant.PointStruct, 0, len(items))
	for _, v := range items {
		p, err := toPointStruct(v)
		if err != nil {
			return err
		}
		points = append(points, p)
	}

	wait := true
	_, err := QdrantClient.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collection,
		Wait:           &wait,
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("批量写入向量失败: %w", err)
	}
	return nil
}

// ============ 对外封装：检索 ============

// QdrantSearchHit 一次检索命中的结果。
// Content: 切片原文内容（写入时冗余到 payload，检索直接返回，无需回查数据库）
// Score: 相似度得分（Cosine 下 越高越相似，接近 1 最优）
// Payload: 完整元数据（含 tenant_id/document_id/chunk_index/content 等）
type QdrantSearchHit struct {
	Content string
	Score   float32
	Payload map[string]interface{}
}

// tenantIDFilter 构造"tenant_id 等值匹配"的过滤条件。
// ⚠️ 供检索强制使用——把过滤条件写死在这里，业务层传进来也不信，杜绝跨租户泄漏。
// 用整数 Match_Integer 精确匹配（tenant_id 是 int 类型 payload）。
func tenantIDFilter(tenantID uint64) *qdrant.Filter {
	return &qdrant.Filter{
		Must: []*qdrant.Condition{
			{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "tenant_id",
						Match: &qdrant.Match{
							MatchValue: &qdrant.Match_Integer{Integer: int64(tenantID)},
						},
					},
				},
			},
		},
	}
}

// SearchVectors 查询与 query 向量最相似的 topK 个点，返回内容列表与相似度分数。
// ⚠️⚠️ 多租户隔离核心（重中之重）：
//   - 必须传 tenantID，并在查询条件中「强制」带上 tenant_id 过滤；
//   - 过滤条件由本函数内部构造（tenantIDFilter），传入的 tenantID 即为过滤值，
//     即使业务层处理不当，也绝不可能查到其他租户的向量。
//
// 返回按相似度降序排列的 {Content, Score}。Content 直接取自 payload（写入时冗余存储），
// 检索后无需再查数据库取原文，降低 RAG 检索环节的延迟与依赖。
func SearchVectors(ctx context.Context, query []float32, tenantID uint64, topK int) ([]QdrantSearchHit, error) {
	collection := config.GlobalConfig.Qdrant.CollectionName

	limit := uint64(topK)
	req := &qdrant.QueryPoints{
		CollectionName: collection,
		// 构造 query：dense 查询向量
		Query: qdrant.NewQueryNearest(qdrant.NewVectorInput(query...)),
		Limit: &limit,
		// ⚠️ 强制 tenant_id 过滤：只有当前租户的向量会被检索
		Filter: tenantIDFilter(tenantID),
		// 检索需带回 payload（content/document_id/chunk_index 等）
		WithPayload: qdrant.NewWithPayload(true),
		// 不返回向量，只带 payload（节省带宽；业务只用 payload 里的元数据）
		WithVectors: qdrant.NewWithVectors(false),
	}

	resp, err := QdrantClient.GetPointsClient().Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}

	// 组装结果
	hits := make([]QdrantSearchHit, 0, len(resp.GetResult()))
	for _, scored := range resp.GetResult() {
		p := structToMap(scored.GetPayload())
		// Content 是写入时的必填元数据，从 payload 取出返回
		content, _ := p["content"].(string)
		hits = append(hits, QdrantSearchHit{
			Score:   scored.GetScore(),
			Content: content,
			Payload: p,
		})
	}
	return hits, nil
}

// ============ 对外封装：删除 ============

// DeleteVectorByID 从集合删除指定 ID 的向量点。
// （删除文档时，连带删除其向量，避免孤儿向量）
func DeleteVectorByID(ctx context.Context, id uint64) error {
	collection := config.GlobalConfig.Qdrant.CollectionName
	wait := true
	_, err := QdrantClient.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collection,
		Wait:           &wait,
		Points: qdrant.NewPointsSelectorIDs(
			[]*qdrant.PointId{qdrant.NewIDNum(id)},
		),
	})
	if err != nil {
		return fmt.Errorf("删除向量失败: %w", err)
	}
	return nil
}

// structToMap 把 Qdrant 返回的 payload（map[string]*Value）转成普通 map[string]interface{}。
// 供上层直接读取字段（如 text、document_id）。
func structToMap(payload map[string]*qdrant.Value) map[string]interface{} {
	out := make(map[string]interface{}, len(payload))
	for k, v := range payload {
		out[k] = valueToInterface(v)
	}
	return out
}

// valueToInterface 把 Qdrant 的动态类型 Value 转成 Go 的 interface{}。
// 仅支持文本/整型/浮点等常见标量（Embedding payload 用到的类型足够）。
func valueToInterface(v *qdrant.Value) interface{} {
	switch val := v.GetKind().(type) {
	case *qdrant.Value_StringValue:
		return val.StringValue
	case *qdrant.Value_IntegerValue:
		return val.IntegerValue
	case *qdrant.Value_DoubleValue:
		return val.DoubleValue
	case *qdrant.Value_BoolValue:
		return val.BoolValue
	case *qdrant.Value_StructValue:
		return structToMap(val.StructValue.GetFields())
	case *qdrant.Value_ListValue:
		var arr []interface{}
		for _, item := range val.ListValue.GetValues() {
			arr = append(arr, valueToInterface(item))
		}
		return arr
	default:
		return nil
	}
}
