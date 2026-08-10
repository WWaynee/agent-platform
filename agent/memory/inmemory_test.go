package memory

import "testing"

func TestInMemoryMemory(t *testing.T) {
	m := NewInMemoryMemory()
	const tenant = uint64(1)
	sid := "session-A"

	// 空历史应返回 nil
	if h := m.GetHistory(tenant, sid); h != nil {
		t.Fatalf("空会话历史应为 nil，got %+v", h)
	}

	// 存入两条消息
	m.AddMessage(tenant, sid, ChatMessage{Role: RoleUser, Content: "你好"})
	m.AddMessage(tenant, sid, ChatMessage{Role: RoleAssistant, Content: "你好，有什么可以帮你？"})

	// 存进去要能取出来
	hist := m.GetHistory(tenant, sid)
	if len(hist) != 2 {
		t.Fatalf("历史长度应为 2，got %d", len(hist))
	}
	if hist[0].Content != "你好" || hist[1].Content != "你好，有什么可以帮你？" {
		t.Fatalf("历史内容不符: %+v", hist)
	}

	// 不同 session 相互隔离
	m.AddMessage(tenant, "session-B", ChatMessage{Role: RoleUser, Content: "B 的第一个消息"})
	if n := len(m.GetHistory(tenant, "session-B")); n != 1 {
		t.Fatalf("session-B 应有 1 条，got %d", n)
	}
	if n := len(m.GetHistory(tenant, sid)); n != 2 {
		t.Fatalf("session-A 应仍为 2 条（会话隔离），got %d", n)
	}

	// 多租户隔离：相同 sessionID、不同租户互不混存（第2小步 key 含 tenant_id）
	m.AddMessage(2, sid, ChatMessage{Role: RoleUser, Content: "租户2的会话A"})
	if n := len(m.GetHistory(tenant, sid)); n != 2 {
		t.Fatalf("租户1 的 session-A 应仍为 2 条（不同租户不混存），got %d", n)
	}
	if n := len(m.GetHistory(2, sid)); n != 1 {
		t.Fatalf("租户2 的 session-A 应有 1 条，got %d", n)
	}

	// Truncate：截断只保留最近 maxTokens 条
	m.AddMessage(tenant, sid, ChatMessage{Role: RoleUser, Content: "第三句"})
	m.Truncate(tenant, sid, 2)
	if h := m.GetHistory(tenant, sid); len(h) != 2 || h[0].Content != "你好，有什么可以帮你？" {
		t.Fatalf("Truncate 后应只保留最近 2 条, got %+v", h)
	}

	// 返回副本：调用方修改不应影响内部存储
	got := m.GetHistory(tenant, sid)
	got[0].Content = "篡改"
	if h := m.GetHistory(tenant, sid); h[0].Content == "篡改" {
		t.Fatal("GetHistory 应返回副本，内部数据不应被调用方篡改")
	}

	// Clear：清空后历史应为空
	m.Clear(tenant, sid)
	if h := m.GetHistory(tenant, sid); h != nil {
		t.Fatalf("Clear 后历史应为空，got %+v", h)
	}
}
