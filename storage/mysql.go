package storage

import (
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"

	"agent-platform/config"
)

// DB 全局数据库连接（GORM），业务代码直接使用
var DB *gorm.DB

// InitMySQL 从 config 读取数据库配置，初始化 GORM + MySQL 连接
// 连接成功后将连接赋值给全局变量 DB，供业务代码直接使用
func InitMySQL() error {
	cfg := config.GlobalConfig.MySQL

	// 拼接 DSN
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.DBName,
	)

	// 用 GORM 连接数据库；注入自定义 logger：
	// GORM 语句级别（含自动迁移/查询）执行后经 Trace 回调，实现 DB 慢查询与错误日志落到 observability。
	obsLog := &obsDBLogger{logLevel: gormLogger.Warn}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: obsLog})
	if err != nil {
		return fmt.Errorf("MySQL 连接失败: %w", err)
	}

	// 连接池配置
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取底层 SQL DB 失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetMaxIdleConns(10)

	// 赋值给全局变量，供业务代码直接使用
	DB = db
	return nil
}
