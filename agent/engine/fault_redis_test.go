package engine

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// ============ 第3步 · Redis 挂掉时的容错（集成测试）============
//
// 自测标准：Redis 挂了 → 服务不崩、不 panic。
//
// RedisMemory 的方法不返回 error（Memory 接口签名如此）：连接失败时
// GetHistory 返回 nil、AddMessage 静默失败，因此引擎读不到历史照常跑、
// 写不回历史也不 panic。本测试验证：把 Redis client 主动关闭（模拟故障）
// 后，引擎 Run 仍能完成且不 panic。

// TestFault_RedisDown_EngineNoPanic
// 需要真实 Redis（本机 docker compose 起即可）。连不上则跳过。
// 验证：Redis 断开后调用引擎 Run，不 panic，且能给到回答（历史为空仍正常完成）。
func TestFault_RedisDown_EngineNoPanic(t *testing.T) {
	rdb := newEngineTestRedis(t)
	if rdb == nil {
		return // 已 Skip
	}

	// 用一个"会出错的 LLM"失败降级可区分信号；这里用正常 fallback 便于断言非 panic。
	// 为避免依赖真实 LLM，用一个固定返回 final_answer 的假 LLM。
	llm := &sequenceLLM{replies: []string{`{"action":"final_answer","action_input":"正常回答"}`}}

	// 直接用 RedisMemory 作为底层（真实 Redis 客户端）
	baseMem := memory.NewRedisMemory(rdb)
	// 构造引擎：直接用 InMemoryMemory 做兜底内存，但通过 CompressingMemory 包裹 Redis，
	// 这里重点是"底层 Redis 断连时引擎不 panic"。
	e := NewReActEngine(llm, toolmanager.NewToolManager(), baseMem, "你是一个助手")

	// 先把 client 关闭，模拟 Redis 挂掉/断连
	_ = rdb.Close()

	// 断连后 Run：应不 panic，返回正常兜底回答（历史读不到→空，写入静默失败）
	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "redis-down"}, "redis挂了你还能答吗")
	})
	if err != nil {
		t.Fatalf("Redis 挂掉不应让引擎返回错误, got err=%v", err)
	}
	if resp == nil || resp.Answer != "正常回答" {
		t.Fatalf("Redis 挂掉后引擎仍应正常完成回答, got %+v", resp)
	}
}

// newEngineTestRedis 连接本机 Redis；连不上则 Skip 并返回 nil。
func newEngineTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8942"
	}
	pass := os.Getenv("REDIS_PASSWORD")
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: pass, DB: 0})
	ctx, cancel := context.WithTimeout(context.Background(), 1500e6)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Skipf("跳过 Redis 容错测试：无法连接 Redis(%s): %v", addr, err)
		return nil
	}
	return rdb
}
