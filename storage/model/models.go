package model

import (
	"time"

	"gorm.io/gorm"
)

// 租户表 tenant
type Tenant struct {
	ID            uint64 `gorm:"primaryKey"`
	Name          string `gorm:"size:128;not null;comment:租户名称"`
	Status        int8   `gorm:"default:1;comment:0禁用 1启用"`
	QuotaLlmToken int64  `gorm:"default:0;comment:LLM token配额"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

// 用户表 user
type User struct {
	ID           uint64 `gorm:"primaryKey"`
	TenantID     uint64 `gorm:"uniqueIndex:idx_tenant_user;not null;comment:租户ID"`
	Username     string `gorm:"size:64;not null;uniqueIndex:idx_tenant_user;comment:用户名"`
	PasswordHash string `gorm:"size:256;not null;comment:密码哈希，不存明文"`
	Role         string `gorm:"size:32;not null;comment:admin / member"`
	Status       int8   `gorm:"default:1"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

// document 文档元数据表
type Document struct {
	ID             uint64 `gorm:"primaryKey"`
	TenantID       uint64 `gorm:"index;not null"`
	UserID         uint64 `gorm:"index;comment:上传者用户ID"`
	Name           string `gorm:"size:256;not null;comment:文档名称"`
	MinioObjectKey string `gorm:"size:512;not null;comment:minio存储key"`
	Status         string `gorm:"size:32;not null;comment:pending/processing/success/fail"`
	ErrorMsg       string `gorm:"type:text;comment:失败原因（仅 failed 状态时记录）"`
	Size           int64  `gorm:"comment:文件字节大小"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

// agent_task 异步任务表
type AgentTask struct {
	ID        uint64 `gorm:"primaryKey"`
	TenantID  uint64 `gorm:"index;not null"`
	TaskType  string `gorm:"size:64;not null;comment:document_parse"`
	BizID     uint64 `gorm:"comment:关联业务id，document id"`
	Status    string `gorm:"size:32;not null;comment:pending/running/success/failed"`
	ErrorMsg  string `gorm:"type:text;comment:错误信息"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// session 会话元数据表
type Session struct {
	ID        uint64 `gorm:"primaryKey"`
	TenantID  uint64 `gorm:"index;not null"`
	UserID    uint64 `gorm:"index;not null"`
	Title     string `gorm:"size:256;comment:会话标题"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// audit_log 审计日志表
type AuditLog struct {
	ID        uint64 `gorm:"primaryKey"`
	TenantID  uint64 `gorm:"index;not null"`
	UserID    uint64 `gorm:"index"`
	Operation string `gorm:"size:128;not null;comment:操作类型"`
	TraceID   string `gorm:"size:128;comment:全链路traceId"`
	Content   string `gorm:"type:text"`
	CreatedAt time.Time
}

// tenant_tool_config 租户工具权限配置
type TenantToolConfig struct {
	ID       uint64 `gorm:"primaryKey"`
	TenantID uint64 `gorm:"index;not null"`
	ToolName string `gorm:"size:128;not null;comment:工具标识，knowledge_retrieve"`
	// 不要用 `default:true`：bool 零值是 false，加 default 后 gorm 会把显式
	// 关闭(false)当"未赋值"而替换成列默认值 true，导致关闭权限校验失效。
	IsEnable  bool `gorm:"comment:是否开启"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// 获取全部模型，用于自动迁移
func AllModels() []interface{} {
	return []interface{}{
		&Tenant{},
		&User{},
		&Document{},
		&AgentTask{},
		&Session{},
		&AuditLog{},
		&TenantToolConfig{},
	}
}
