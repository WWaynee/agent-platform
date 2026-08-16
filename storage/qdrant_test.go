package storage

import (
	"testing"

	"github.com/qdrant/go-client/qdrant"
)

// extractFilterLock 从 searchFilter 生成的 Filter 里抽取"强制租户 + 可选文档范围"两个关键约束，
// 便于断言多租户隔离底线与 document_ids 叠加逻辑（不深比较整个结构，只看关键过滤条件）。
// 返回 (tenantID, documentIDs 的切片或 nil)。
func extractFilterLock(f *qdrant.Filter) (tenantID int64, docIDs []int64) {
	docIDs = nil
	for _, cond := range f.GetMust() {
		cf, ok := cond.GetConditionOneOf().(*qdrant.Condition_Field)
		if !ok {
			continue
		}
		field := cf.Field
		switch field.GetKey() {
		case "tenant_id":
			tenantID = field.GetMatch().GetMatchValue().(*qdrant.Match_Integer).Integer
		case "document_id":
			integ := field.GetMatch().GetMatchValue().(*qdrant.Match_Integers).Integers
			docIDs = append(docIDs, integ.GetIntegers()...)
		}
	}
	return tenantID, docIDs
}

// TestSearchFilter 覆盖需求单 5.1 与 5.4 的多租户隔离底线 + 文档范围叠加：
//  1. tenant_id 过滤始终强制存在（business 传错也不信）；
//  2. 不传 documentIDs → 只有 tenant 过滤（全租户，兼容老逻辑）；
//  3. 传 documentIDs → 在 tenant 之上叠加 document_id in [...].
func TestSearchFilter(t *testing.T) {
	t.Run("不传 documentIDs = 仅强制 tenant 过滤", func(t *testing.T) {
		f := searchFilter(9988, nil)
		tenant, docIDs := extractFilterLock(f)
		if tenant != 9988 {
			t.Errorf("tenant_id 应为 9988，实际 %d", tenant)
		}
		if docIDs != nil || len(f.GetMust()) != 1 {
			t.Errorf("不传 documentIDs 时不应叠加 document 过滤，实际 docIDs=%v 条件数=%d", docIDs, len(f.GetMust()))
		}
	})

	t.Run("传 documentIDs = 强制 tenant + document in 过滤", func(t *testing.T) {
		f := searchFilter(42, []uint64{1, 2, 3})
		tenant, docIDs := extractFilterLock(f)
		if tenant != 42 {
			t.Errorf("tenant_id 应为 42，实际 %d", tenant)
		}
		if len(docIDs) != 3 || docIDs[0] != 1 || docIDs[1] != 2 || docIDs[2] != 3 {
			t.Errorf("document_ids 应为 [1 2 3]，实际 %v", docIDs)
		}
		if len(f.GetMust()) != 2 {
			t.Errorf("Must 应为 tenant+document 两条，实际 %d 条", len(f.GetMust()))
		}
	})

	t.Run("单文档 = tenant + document in [单值]", func(t *testing.T) {
		f := searchFilter(7, []uint64{99})
		_, docIDs := extractFilterLock(f)
		if len(docIDs) != 1 || docIDs[0] != 99 {
			t.Errorf("应只含文档ID 99，实际 %v", docIDs)
		}
	})

	t.Run("隔离不因 documentIDs 削弱：任意调用都带 tenant", func(t *testing.T) {
		// 用别的租户混入也不影响本租户过滤（隔离在 storage 死守）
		for _, tid := range []uint64{1, 2, 9988} {
			f := searchFilter(tid, []uint64{5})
			tenant, _ := extractFilterLock(f)
			if tenant != int64(tid) {
				t.Errorf("tenant 过滤应为发起方 %d，实际 %d", tid, tenant)
			}
		}
	})
}

// TestTenantIDFilter_兼容 保证老入口 tenantIDFilter 仍等效"强制本租户、无文档叠加"。
func TestTenantIDFilter_兼容(t *testing.T) {
	f := tenantIDFilter(2024)
	tenant, docIDs := extractFilterLock(f)
	if tenant != 2024 {
		t.Errorf("tenantIDFilter 应强制 tenant=2024，实际 %d", tenant)
	}
	if docIDs != nil {
		t.Errorf("tenantIDFilter 不应带文档过滤，实际 %v", docIDs)
	}
}
