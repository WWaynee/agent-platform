package main

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"agent-platform/storage/model"
)

func main() {
	// 加载项目根目录 .env 文件
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Printf("加载.env失败: %v\n", err)
		os.Exit(1)
	}

	// 读取环境变量
	mysqlUser := os.Getenv("MYSQL_USER")
	mysqlPwd := os.Getenv("MYSQL_ROOT_PWD")
	mysqlHost := os.Getenv("MYSQL_HOST")
	mysqlPort := os.Getenv("MYSQL_PORT")
	mysqlDb := os.Getenv("MYSQL_DB")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		mysqlUser,
		mysqlPwd,
		mysqlHost,
		mysqlPort,
		mysqlDb,
	)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Printf("数据库连接失败: %v\n", err)
		os.Exit(1)
	}

	err = db.AutoMigrate(model.AllModels()...)
	if err != nil {
		fmt.Printf("迁移建表失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ 所有数据表自动迁移完成！")
}