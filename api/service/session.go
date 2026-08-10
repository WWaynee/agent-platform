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
// 入参：tenantID、userID、标题。返回新会话的 ID。
// 会话 ID 同时作为该会话在 Redis 消息历史里的 session_id，前端对话传 session_id=<ID> 即可续写/定位。
func CreateSession(tenantID, userID uint64, title string) (uint64, error) {
	s := &model.Session{
		TenantID: tenantID,
		UserID:   userID,
		Title:    title,
	}
	if err := storage.CreateSession(s); err != nil {
		return 0, fmt.Errorf("创建会话失败: %w", err)
	}
	return s.ID, nil
}

// GetSessionDetail 按 ID 查询单个会话（带租户过滤）。
// 供对话接口校验"传的 session_id 是否属于当前租户"。
// 记录不存在或属于别的租户时，返回 gorm.ErrRecordNotFound（由调用方统一转"无权访问"）。
func GetSessionDetail(tenantID, id uint64) (*model.Session, error) {
	return storage.GetSessionByID(tenantID, id)
}

// GetSessionList 会话列表。
// 只返回当前用户的会话（tenant + user 双重过滤），按更新时间倒序，分页。
// 返回该页数据切片、总数、以及可能的错误。
func GetSessionList(tenantID, userID uint64, page, pageSize int) ([]model.Session, int64, error) {
	return storage.ListSessions(tenantID, userID, page, pageSize)
}

// DeleteSession 删除会话。
// 流程：
//  1. 校验：按 id + tenant 查会话，必须存在，且属于当前用户（只能删自己的）；
//  2. 软删数据库会话记录（deleted_at 打时间戳）；
//  3. 同时删除 Redis 里的该会话消息历史 —— 否则 DB 删了、Redis 消息还留着浪费内存，
//     两边一起删保持数据一致。
func DeleteSession(tenantID, userID, id uint64) error {
	// 1. 校验存在且属于当前租户
	s, err := storage.GetSessionByID(tenantID, id)
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
	if err := storage.DeleteSession(tenantID, id); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}

	// 3. 同步删除 Redis 里的会话消息历史（Redis 未初始化则跳过，不阻塞删除）
	if storage.RDB != nil {
		key := sessionRedisKey(tenantID, s.ID)
		if err := storage.RDB.Del(context.Background(), key).Err(); err != nil {
			return fmt.Errorf("删除会话消息历史失败: %w", err)
		}
	}

	return nil
}
