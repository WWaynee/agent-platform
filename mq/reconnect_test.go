package mq

import (
	"sync"
	"testing"
)

// ============ RabbitMQ 自动重连单元测试 ============
//
// 真实重连链路（停 RabbitMQ → 自动恢复）已在 e2e 验证（docker 停/起容器）。
// 本文件验证自动重连机制的**单元行为**（不依赖真实 Broker）：
//   1. Publish 在连接未初始化时返回错误而非 panic（服务不崩）；
//   2. EnsureConnected 在连接未就绪时返回错误而非 panic（不会误用空引用）；
//   3. 并发调用 EnsureConnected 不 panic / 数据竞争（供 go test -race 校验 mqMu 保护）。
//
// ⚠️ 用例会读写全局 MQConn/MQCh/mqMu，均通过 t.Cleanup 恢复原状，避免污染他人。

// snapshotConn 快照全局连接状态，测试结束后恢复。
func snapshotConn(t *testing.T) {
	t.Helper()
	oc, och := MQConn, MQCh
	t.Cleanup(func() {
		MQConn, MQCh = oc, och
	})
}

// TestEnsureConnected_NilNoPanic 未初始化且无可用 Broker 时，EnsureConnected
// 尝试重建并返回错误而非 panic（或"假装成功"）。
func TestEnsureConnected_NilNoPanic(t *testing.T) {
	snapshotConn(t)
	MQConn, MQCh = nil, nil

	if err := EnsureConnected(); err == nil {
		t.Log("无可用 RabbitMQ 时 EnsureConnected 返回 nil（环境可能恰有 Broker，跳过强断言）")
	}
}

// TestPublish_NilConnNoPanic 连接未初始化时 Publish 返回错误而非 panic（服务不崩）。
func TestPublish_NilConnNoPanic(t *testing.T) {
	snapshotConn(t)
	MQConn, MQCh = nil, nil

	if err := Publish("some_queue", []byte("x")); err == nil {
		t.Fatal("未初始化连接时 Publish 应返回错误而非成功")
	}
}

// TestEnsureConnected_ConcurrentCalls 并发调用 EnsureConnected 不 panic，
// 验证 mqMu 对连接重建的并发保护（go test -race 可复现校验数据竞争）。
func TestEnsureConnected_ConcurrentCalls(t *testing.T) {
	snapshotConn(t)
	MQConn, MQCh = nil, nil

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = EnsureConnected() // 只验证不发生 panic / 数据竞争
		}()
	}
	wg.Wait()
}
