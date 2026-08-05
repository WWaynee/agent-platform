package storage

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RDB 全局 Redis 客户端，业务代码直接使用
var RDB *redis.Client

// InitRedis 初始化 Redis 客户端连接
func InitRedis(host string, port int, password string) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       0,
	})

	// 赋值给全局变量，供业务代码直接使用
	RDB = rdb
	return rdb, nil
}
