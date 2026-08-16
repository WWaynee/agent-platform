package service

import (
	"reflect"
	"testing"
)

// TestNormalizeSearchParams 覆盖需求单 5.1 的检索边界收敛：
//
//	top_k 未传/=0 → 默认3；超大 → 压到10；document_ids 空/全无效/混入无效 → 剔除非正项。
func TestNormalizeSearchParams(t *testing.T) {
	// ⚠️ 说明：normalizeSearchParams 的 documentIDs 是 []uint64（Go 强类型），
	// 负数在编译期即被拒绝，真正会走到"剔除非正项"分支的是 `0`（HTTP/JSON 可能反序列化出 0）。
	// 负数在入参这一层不存在，故测试只覆盖 0 及不存在的超大 ID 的剔除语义。
	cases := []struct {
		name       string
		topK       int
		docIDs     []uint64
		wantTopK   int
		wantDocIDs []uint64
	}{
		{"top_k 未传(=0) 取默认3", 0, nil, defaultSearchTopK, []uint64{}},
		{"top_k 负数(=0 等价) 取默认3", -5, nil, defaultSearchTopK, []uint64{}},
		{"top_k 正常值透传", 4, nil, 4, []uint64{}},
		{"top_k 超大压到上限10", 100, nil, maxSearchTopK, []uint64{}},
		{"document_ids 空数组=全租户", 3, []uint64{}, 3, []uint64{}},
		// ⚠️ 语法剔除只处理 `<=0`；不存在的正 ID（如 999999）无法在净化层判"不存在"，
		//   会保留并传给 document_id in 查询——由向量检索返回空（安全，不会误伤其他租户）。
		{"document_ids 全为0 剔除后为空=全租户", 3, []uint64{0, 0}, 3, []uint64{}},
		{"document_ids 混0与不存在正ID: 剔除0、保留正ID", 3, []uint64{0, 999999}, 3, []uint64{999999}},
		{"document_ids 有效+0混: 剔除0只留有效", 3, []uint64{1, 0}, 3, []uint64{1}},
		{"document_ids 有效+不存在混: 两正ID都保留", 3, []uint64{1, 999}, 3, []uint64{1, 999}},
		{"document_ids 含0与有效混: 剔除0", 3, []uint64{0, 7, 2}, 3, []uint64{7, 2}},
		{"document_ids 全有效保留", 3, []uint64{11, 22}, 3, []uint64{11, 22}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotTopK, gotIDs := normalizeSearchParams(c.topK, c.docIDs)
			if gotTopK != c.wantTopK {
				t.Errorf("topK = %d, want %d", gotTopK, c.wantTopK)
			}
			if !reflect.DeepEqual(gotIDs, c.wantDocIDs) {
				t.Errorf("document_ids = %v, want %v", gotIDs, c.wantDocIDs)
			}
		})
	}

	t.Log("✅ 检索边界收敛全部符合预期（默认3 / 上限10 / document_ids 剔除非正项 / 全无效退回全租户）")
}

// TestPayloadString 验证从 payload 取字符串字段的健壮性：缺失/类型不符 → 空串不报错。
func TestPayloadString(t *testing.T) {
	p := map[string]interface{}{"document_name": "销售合同A"}
	if got := payloadString(p, "document_name"); got != "销售合同A" {
		t.Errorf("应取到文档名，实际 %q", got)
	}
	if got := payloadString(p, "不存在字段"); got != "" {
		t.Errorf("缺失字段应为空串，实际 %q", got)
	}
	// document_name 存成非字符串（如旧数据 float64）时应回退空串、不崩
	q := map[string]interface{}{"document_name": float64(3)}
	if got := payloadString(q, "document_name"); got != "" {
		t.Errorf("类型不符应为空串，实际 %q", got)
	}
	if got := payloadString(nil, "document_name"); got != "" {
		t.Errorf("nil payload 应为空串，实际 %q", got)
	}
	t.Log("✅ payloadString：提取文档名 & 缺失/类型不符安全回退空串")
}

// TestPayloadUint64 验证从 payload 取整型字段处理多种数值类型。
func TestPayloadUint64(t *testing.T) {
	if got := payloadUint64(map[string]interface{}{"document_id": int64(7)}, "document_id"); got != 7 {
		t.Errorf("int64 应为 7，实际 %d", got)
	}
	if got := payloadUint64(map[string]interface{}{"document_id": float64(8.0)}, "document_id"); got != 8 {
		t.Errorf("float64 应为 8，实际 %d", got)
	}
	if got := payloadUint64(map[string]interface{}{"chunk_index": int64(-1)}, "chunk_index"); got != uint64(18446744073709551615) {
		t.Errorf("int64 -1 转换预期（非负语义由业务保证），实际 %d", got)
	}
	if got := payloadUint64(map[string]interface{}{}, "不存在"); got != 0 {
		t.Errorf("缺失字段应为 0，实际 %d", got)
	}
}
