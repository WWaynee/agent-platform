package service

import (
	"testing"

	"agent-platform/mq"
	"agent-platform/storage"
)

// TestOverallUp 验证整体健康判定：全部 up → 健康；任一 down → 不健康。
func TestOverallUp(t *testing.T) {
	up := checkResult{status: StatusUp}
	dn := checkResult{status: StatusDown, errMsg: "某组件挂了"}

	if !overallUp(map[string]checkResult{
		"mysql": up, "redis": up, "minio": up, "qdrant": up, "rabbitmq": up,
	}) {
		t.Error("全部 up 应判定为健康")
	}
	t.Run("任一 down 即整体不健康", func(t *testing.T) {
		for _, name := range []string{"mysql", "redis", "minio", "qdrant", "rabbitmq"} {
			comps := map[string]checkResult{
				"mysql": up, "redis": up, "minio": up, "qdrant": up, "rabbitmq": up,
			}
			comps[name] = dn
			if overallUp(comps) {
				t.Errorf("组件 %s down 时应整体不健康", name)
			}
		}
	})
	t.Run("IsHealthy 与 status 一致", func(t *testing.T) {
		if !(HealthReport{Status: "healthy"}).IsHealthy() {
			t.Error("status=healthy 应健康")
		}
		if (HealthReport{Status: "unhealthy"}).IsHealthy() {
			t.Error("status=unhealthy 应不健康")
		}
	})
	t.Log("✅ 整体判定：全部 up=healthy；任一 down=unhealthy")
}

// TestHealth_ComponentsDownWhenNotInit 验证：依赖未初始化（如服务被停）时，
// 各组件健康检查返回 down + 错误信息；CheckAll 整体 unhealthy。
// 用全局对象置 nil 模拟依赖不可用（等价于"故意停掉某依赖"）。
func TestHealth_ComponentsDownWhenNotInit(t *testing.T) {
	origDB, origRDB := storage.DB, storage.RDB
	origMinio, origQdrant := storage.MinioClient, storage.QdrantClient
	origConn, origCh := mq.MQConn, mq.MQCh
	defer func() {
		storage.DB, storage.RDB, storage.MinioClient, storage.QdrantClient = origDB, origRDB, origMinio, origQdrant
		mq.MQConn, mq.MQCh = origConn, origCh
	}()

	// 全部依赖置为未初始化
	storage.DB = nil
	storage.RDB = nil
	storage.MinioClient = nil
	storage.QdrantClient = nil
	mq.MQConn, mq.MQCh = nil, nil

	report := CheckAll()

	if report.IsHealthy() {
		t.Fatal("依赖全未初始化时应整体不健康")
	}
	if report.Status != "unhealthy" {
		t.Fatalf("status 应为 unhealthy，实际 %q", report.Status)
	}

	// components 应为字符串值 map，包含全部 5 个组件且都是 "down"；
	// errors map 应包含每个 down 组件的错误信息。
	for _, name := range []string{"mysql", "redis", "minio", "qdrant", "rabbitmq"} {
		status, ok := report.Components[name]
		if !ok {
			t.Errorf("缺少组件 %s", name)
			continue
		}
		if status != StatusDown {
			t.Errorf("%s 未初始化 status 应为 down，实际 %q", name, status)
		}
		if report.Errors[name] == "" {
			t.Errorf("%s down 应带错误信息 (errors[%s])", name, name)
		}
	}
	if len(report.Components) != 5 {
		t.Errorf("应包含 5 个组件，实际 %d", len(report.Components))
	}
	if len(report.Errors) != 5 {
		t.Errorf("应有 5 个 down 组件的错误信息，实际 %d", len(report.Errors))
	}

	t.Logf("✅ 依赖不可用时各组件 down + 错误信息，status=unhealthy，组件数=%d", len(report.Components))
}
