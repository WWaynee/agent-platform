package engine

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agent-platform/agent/memory"
)

// ============ 摘要压缩函数 ============
//
// CompressHistory 把某会话的超长历史压缩成「摘要 + 最近 keepRecentRounds 轮」并写回 Memory。
// 依赖上一步定好的压缩策略（阈值 / 保留轮数 / 结构见 compressor.go）。
//
// 流程：
//  1. 把历史分成两段：旧消息（前半，需压缩）、新消息（最近 keepRecentRounds 轮，保留原文）；
//  2. 把旧消息拼起来发给 LLM，生成一段中文摘要；
//  3. 新历史 = [一条 system 摘要消息] + [最近 keepRecentRounds 轮消息]；
//  4. 把压缩后的历史写回 Memory（先清空该会话，再按序写回）。
//
// 注意点：
//   - 同步执行：对话频率不高，同步做简单可靠。
//   - 压缩失败降级：LLM 摘要失败时不返回错误中断，而是降级为「直接丢弃旧消息」，
//     只保留最近几轮原文——至少保证继续对话可用、不撑爆上下文窗口。
//   - 避免频繁压缩：若历史第一条已是 system 摘要（说明刚压缩过），即便又超长也不立即
//     再次套娃压缩，留待后续触发，防止反复摘要磨平信息。
func (e *ReActEngine) CompressHistory(ctx context.Context, actx AgentContext, history []memory.ChatMessage) ([]memory.ChatMessage, error) {
	// 已压缩过（历史以 system 摘要开头）→ 不重复套娃压缩
	if len(history) > 0 && history[0].Role == memory.RoleSystem {
		return history, nil
	}

	// 1. 拆分新旧
	split := splitKeepRecent(history, keepRecentRounds)
	oldPart := history[:split]
	recent := history[split:]
	if len(oldPart) == 0 {
		// 没有可压缩的旧消息，无需处理
		return history, nil
	}

	// 2. 生成摘要（同步；失败走降级）
	summary, err := e.summarizePart(ctx, oldPart)
	if err != nil {
		// 降级：直接丢旧消息，保留最近 N 轮原文
		log.Printf("[compress] 摘要生成失败，降级为丢弃旧消息保留最近 %d 轮: %v", keepRecentRounds, err)
		compressed := append([]memory.ChatMessage(nil), recent...)
		e.writeHistory(actx, compressed)
		return compressed, nil
	}

	// 3. 组装新历史 = [system 摘要] + 最近 N 轮
	compressed := append(
		[]memory.ChatMessage{{Role: memory.RoleSystem, Content: fmt.Sprintf("对话历史摘要：%s", summary)}},
		recent...,
	)

	// 4. 写回 Memory（Redis 实现即写回 Redis）
	e.writeHistory(actx, compressed)
	return compressed, nil
}

// ============ 摘要生成（实现 memory.Summarizer，供 Memory 层注入） ============

// summarizeInstruction 让 LLM 生成历史摘要的系统指令。
const summarizeInstruction = `请把以下对话历史压缩成一段简洁的摘要，保留关键信息和上下文，不要丢失重要细节。摘要用中文。只输出摘要正文，不要任何解释或前后缀：`

// Summarize 实现 memory.Summarizer 接口：
// 把一段历史拼成可读文本，发给 LLM 生成中文摘要。
// 供 Memory 层（CompressingMemory）注入使用，也供 engine 自身 summarizePart 复用同一 prompt。
func (e *ReActEngine) Summarize(msgs []memory.ChatMessage) (string, error) {
	return e.summarizeMessages(context.Background(), msgs)
}

// summarizePart 把一段历史拼成可读文本，发给 LLM 生成中文摘要。
func (e *ReActEngine) summarizePart(ctx context.Context, msgs []memory.ChatMessage) (string, error) {
	return e.summarizeMessages(ctx, msgs)
}

// summarizeMessages 核心实现：拼接历史 → 调用 LLM → 返回摘要正文。
func (e *ReActEngine) summarizeMessages(ctx context.Context, msgs []memory.ChatMessage) (string, error) {
	var sb strings.Builder
	sb.WriteString(summarizeInstruction)
	sb.WriteString("\n\n")
	for _, m := range msgs {
		sb.WriteString(fmt.Sprintf("%s: %s\n", roleLabel(m.Role), m.Content))
	}

	resp, err := e.LLMClient.Chat(ctx, ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "你是对话摘要助手，擅长把长对话浓缩为简洁准确的中文摘要。"},
			{Role: "user", Content: sb.String()},
		},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
}

// writeHistory 把整个历史序列写回 Memory（先清空该会话，再按序逐条写）。
func (e *ReActEngine) writeHistory(actx AgentContext, history []memory.ChatMessage) {
	e.Memory.Clear(actx.TenantID, actx.SessionID)
	for _, m := range history {
		e.Memory.AddMessage(actx.TenantID, actx.SessionID, m)
	}
}

// roleLabel 角色转中文标签，便于摘要 prompt 可读。
func roleLabel(role memory.Role) string {
	switch role {
	case memory.RoleUser:
		return "用户"
	case memory.RoleAssistant:
		return "助手"
	case memory.RoleTool:
		return "工具"
	default:
		return "系统"
	}
}

// 编译期断言：ReActEngine 实现 memory.Summarizer，可直接注入 CompressingMemory。
var _ memory.Summarizer = (*ReActEngine)(nil)
