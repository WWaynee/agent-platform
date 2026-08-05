package storage

import (
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// DB 全局数据库连接（GORM）
var DB *gorm.DB

// RDB 全局 Redis 客户端
var RDB *redis.Client

// InitMySQL 初始化 GORM + MySQL 连接
func InitMySQL(host string, port int, user, password, dbname string) (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		user, password, host, port, dbname)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("MySQL 连接失败: %w", err)
	}

	// 连接池配置
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层 SQL DB 失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetMaxIdleConns(10)

	DB = db
	return db, nil
}

// InitRedis 初始化 Redis 客户端连接
func InitRedis(host string, port int, password string) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       0,
	})

	// 简单验证连接（此处不实际 PING，由 main 决定是否验证，避免依赖运行时中间件）
	RDB = rdb
	return rdb, nil
}
