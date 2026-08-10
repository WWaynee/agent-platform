package memory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisMemory 集成测试 —— RedisMemory 实现 Memory 接口的四个方法，且 key 带 tenant_id。
//
// 需要真实 Redis（本机 docker compose 起即可）。通过环境变量 REDIS_ADDR / REDIS_PASSWORD 指定，
// 默认值指向本地默认 Redis。若连不上则跳过（不破坏无 Redis 环境的 go test ./...）。
func newTestRedis(t *testing.T) *RedisMemory {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8942"
	}
	pwd := os.Getenv("REDIS_PASSWORD")
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: pwd, DB: 9}) // 用独立 db 隔离测试数据
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("跳过 RedisMemory 测试：无法连接 Redis(%s): %v", addr, err)
	}
	return &RedisMemory{ctx: context.Background(), rdb: rdb}
}

// 每个断言用唯一 sessionID（时间戳），避免重复执行时在真实 Redis 上累积脏数据。
func uniqSID(prefix string) string {
	return prefix + "-" + time.Now().Format("150405.000000000")
}

func TestRedisMemory_GetAddAndIsolateByTenant(t *testing.T) {
	m := newTestRedis(t)
	t.Cleanup(func() { _ = m.rdb.Close() })
	const tenant = uint64(88)
	sid := uniqSID("k-wayne")

	// 空历史返回 nil
	if h := m.GetHistory(tenant, sid); h != nil {
		t.Fatalf("空会话历史应为 nil，got %+v", h)
	}

	// 存入两条（user + assistant）
	m.AddMessage(tenant, sid, ChatMessage{Role: RoleUser, Content: "我叫小明"})
	m.AddMessage(tenant, sid, ChatMessage{Role: RoleAssistant, Content: "你好小明"})

	// 取出来、顺序正序、内容一致
	hist := m.GetHistory(tenant, sid)
	if len(hist) != 2 {
		t.Fatalf("历史长度应为 2，got %d", len(hist))
	}
	if hist[0].Role != RoleUser || hist[0].Content != "我叫小明" {
		t.Fatalf("第 1 条不符: %+v", hist[0])
	}
	if hist[1].Role != RoleAssistant || hist[1].Content != "你好小明" {
		t.Fatalf("第 2 条不符: %+v", hist[1])
	}

	// 多租户隔离：同 sessionID、不同 tenant 互不混存（key 带 tenant_id）
	m.AddMessage(989, sid, ChatMessage{Role: RoleUser, Content: "我是租户989"})
	if n := len(m.GetHistory(tenant, sid)); n != 2 {
		t.Fatalf("租户%d 的会话应仍为 2 条（不被租户989污染），got %d", tenant, n)
	}
	if n := len(m.GetHistory(989, sid)); n != 1 {
		t.Fatalf("租户989 的会话应有 1 条，got %d", n)
	}

	// 测试结束清理本测试用到的所有 key，避免在真实 Redis 上留脏数据
	m.Clear(tenant, sid)
	m.Clear(989, sid)
	if h := m.GetHistory(tenant, sid); h != nil {
		t.Fatalf("Clear 后历史应为空，got %+v", h)
	}
}

// TestRedisMemory_TTLRefresh 验证 AddMessage 会刷新 TTL（约7天，每次活跃延续存活期）。
func TestRedisMemory_TTLRefresh(t *testing.T) {
	m := newTestRedis(t)
	t.Cleanup(func() { _ = m.rdb.Close() })
	const tenant = uint64(88)
	sid := uniqSID("k-ttl")
	t.Cleanup(func() { m.Clear(tenant, sid) })

	m.AddMessage(tenant, sid, ChatMessage{Role: RoleUser, Content: "第一条"})
	ttl1, _ := m.rdb.TTL(m.ctx, sessionKey(tenant, sid)).Result()
	if ttl1 <= 0 || ttl1 > 8*24*time.Hour {
		t.Fatalf("TTL 应在 0~8 天内(约7天)，got %v", ttl1)
	}

	// 再次 AddMessage 应刷新 TTL（延长存活时长）
	m.AddMessage(tenant, sid, ChatMessage{Role: RoleUser, Content: "第二条"})
	ttl2, _ := m.rdb.TTL(m.ctx, sessionKey(tenant, sid)).Result()
	if ttl2 < ttl1 {
		t.Fatalf("再次写入应刷新 TTL（ttl2>=ttl1），got ttl1=%v ttl2=%v", ttl1, ttl2)
	}
}
