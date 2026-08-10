package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============ Redis 客户端封装 ============
//
// 作用：把 go-redis 客户端初始化收敛到本文件，供业务代码（会话记忆、限流、
// 摘要缓存等）复用一个全局 RDB。会话记忆改为 Redis 版后，多轮对话的
// 历史存取都走它。
//
// 为什么从 init.go 独立到 redis.go：
//   存取/记忆等后续都会高频依赖 Redis，把客户端初始化收敛到字节职责清晰、
//   便于按名字定位（storage/redis.go）。

// RDB 全局 Redis 客户端，业务代码直接使用
// ⚠️ 使用前必须已调用 InitRedis 成功（RDB 才非 nil），否则会空指针 panic。
var RDB *redis.Client

// InitRedis 初始化 Redis 客户端连接并验证连通性（Ping）。
//
// 参数：
//   - host：地址
//   - port：端口
//   - password：密码（无则空串）
//   - db：逻辑库号（会话记忆可据此分库，避免与其他业务数据互相污染）
//
// 流程：
//  1. 按参数创建 go-redis Client（含连接重试等默认行为）；
//  2. 通过 Ping 实际验证连通性——地址不可达 / 密码错误 / 服务未起都会在此暴露，
//     立即返回错误让主服务启动失败（而不是带病运行后读写才报错）；
//  3. 成功后赋值给全局 RDB，供业务代码直接使用。
//
// ⚠️ 启动流程必须调用（返回错误即 main 应停止）：未初始化 RDB 会导致会话记忆等读写 panic。
func InitRedis(host string, port int, password string, db int) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       db,
	})

	// 连通性测试：给一个短超时，避免连不通时启动长时间挂起
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis 连通性检查失败（host=%s:%d db=%d）: %w", host, port, db, err)
	}

	// 连通正常，赋值给全局，供业务代码使用
	RDB = rdb
	return rdb, nil
}
