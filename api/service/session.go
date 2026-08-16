package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"agent-platform/storage"
	"agent-platform/storage/model"
)

// ============ Service 层：会话业务逻辑 ============
//
// 只做业务编排（校验 / 组合），数据存取交给 storage 层。
// 多租户约束贯穿始终：所有查询/删除都带 tenant_id 过滤，用户只能看到/删除自己的会话。

// sessionRedisKey 生成某租户某会话在 Redis 里的消息历史 key。
// 格式必须与 agent/memory/redis.go 的 sessionKey 一致：
//
//	session:{tenant_id}:{session_id}:messages
//
// 当前约定：会话的自增主键 ID 即 Redis 消息历史里的 session_id（前端对话传 session_id=<ID>）。
func sessionRedisKey(tenantID, id uint64) string {
	return fmt.Sprintf("session:%d:%d:messages", tenantID, id)
}

// CreateSession 创建会话。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 DB 日志带同一链路 ID。
// 入参：ctx、tenantID、userID、标题。返回新会话的 ID。
// 会话 ID 同时作为该会话在 Redis 消息历史里的 session_id，前端对话传 session_id=<ID> 即可续写/定位。
func CreateSession(ctx context.Context, tenantID, userID uint64, title string) (uint64, error) {
	s := &model.Session{
		TenantID: tenantID,
		UserID:   userID,
		Title:    title,
	}
	if err := storage.CreateSession(ctx, s); err != nil {
		return 0, fmt.Errorf("创建会话失败: %w", err)
	}

	// 审计：记录创建会话行为（尽力而为，不影响主流程）。
	// ctx 已由 JWT 中间件种入 tenant_id/user_id/trace_id，RecordAuditLog 会一并落库。
	RecordAuditLog(ctx, "创建会话", fmt.Sprintf("新建会话 %q（ID=%d）", title, s.ID))
	return s.ID, nil
}

// GetSessionDetail 按 ID 查询单个会话（带租户过滤）。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 供对话接口校验"传的 session_id 是否属于当前租户"。
// 记录不存在或属于别的租户时，返回 gorm.ErrRecordNotFound（由调用方统一转"无权访问"）。
func GetSessionDetail(ctx context.Context, tenantID, id uint64) (*model.Session, error) {
	return storage.GetSessionByID(ctx, tenantID, id)
}

// GetSessionList 会话列表。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 只返回当前用户的会话（tenant + user 双重过滤），按更新时间倒序，分页。
// 返回该页数据切片、总数、以及可能的错误。
func GetSessionList(ctx context.Context, tenantID, userID uint64, page, pageSize int) ([]model.Session, int64, error) {
	return storage.ListSessions(ctx, tenantID, userID, page, pageSize)
}

// DeleteSession 删除会话。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage/Redis。
// 流程：
//  1. 校验：按 id + tenant 查会话，必须存在，且属于当前用户（只能删自己的）；
//  2. 软删数据库会话记录（deleted_at 打时间戳）；
//  3. 同时删除 Redis 里的该会话消息历史 —— 否则 DB 删了、Redis 消息还留着浪费内存，
//     两边一起删保持数据一致。
func DeleteSession(ctx context.Context, tenantID, userID, id uint64) error {
	// 1. 校验存在且属于当前租户
	s, err := storage.GetSessionByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("会话不存在")
		}
		return fmt.Errorf("查询会话失败: %w", err)
	}
	// 只能删自己的会话
	if s.UserID != userID {
		return fmt.Errorf("无权删除他人会话")
	}

	// 2. 软删数据库记录
	if err := storage.DeleteSession(ctx, tenantID, id); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}

	// 3. 同步删除 Redis 里的会话消息历史（Redis 未初始化则跳过，不阻塞删除）
	if storage.RDB != nil {
		key := sessionRedisKey(tenantID, s.ID)
		if err := storage.RDB.Del(ctx, key).Err(); err != nil {
			return fmt.Errorf("删除会话消息历史失败: %w", err)
		}
	}

	return nil
}

// GetSessionMessages 查询某会话的对话历史消息（完整原文，冷轨）。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
//
// 多租户/多用户越权防护（先归属后取数）：
//  1. `storage.GetSessionByID(ctx, tenantID, id)`：带租户过滤查询，不存在/属于别的租户
//     统一返回 `gorm.ErrRecordNotFound`（不区分"不存在"与"无权"→ 防横向探测）；
//  2. `s.UserID != userID` 再次校验当前用户是否本人，跨用户一律拒绝；
//     即使租户 B 拿租户 A 的会话 ID 来查，也在这两步被挡回 → "越权查会话"自测点成立。
//
// 取数源：改读 MySQL `chat_messages`（冷轨），不再读 Redis 那份（可能被超长压缩覆盖）。
// 每条消息返回 {role, content, kind}：kind 区分 question/answer/tool_call/tool_result，
// 前端据此展示问答 / 工具指令 / 工具结果。无记录时返回空切片（视为该会话暂无完整历史）。
func GetSessionMessages(ctx context.Context, tenantID, userID, id uint64) ([]map[string]any, error) {
	// 1. 归属校验（带租户过滤 + 本人校验）
	s, err := storage.GetSessionByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("会话不存在")
		}
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}
	if s.UserID != userID {
		return nil, fmt.Errorf("无权访问他人会话")
	}

	// 2. 从 MySQL 冷轨读该会话完整原文（带租户过滤，按 created_at/id 正序）
	list, err := storage.ListChatMessagesBySession(ctx, tenantID, s.ID)
	if err != nil {
		return nil, fmt.Errorf("读取会话消息失败: %w", err)
	}

	// 3. 映射为 {role, content, kind} 返回（kind 为空时由前端按 role 兜底渲染）
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		out = append(out, map[string]any{
			"role":    m.Role,
			"content": m.Content,
			"kind":    m.Kind,
		})
	}
	return out, nil
}
