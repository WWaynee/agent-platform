package storage

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"agent-platform/config"
)

// MinioClient 全局 MinIO 客户端，业务代码直接使用
var MinioClient *minio.Client

// InitMinIO 从 config 读取 MinIO 配置，初始化客户端并确保 bucket 存在
// 连接成功后把客户端赋值给全局变量 MinioClient
func InitMinIO() error {
	cfg := config.GlobalConfig.MinIO

	// 初始化 MinIO 客户端
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return fmt.Errorf("初始化 MinIO 客户端失败: %w", err)
	}

	// 检查 bucket 是否存在，不存在则创建
	ctx := context.Background()
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return fmt.Errorf("检查 bucket 是否存在失败: %w", err)
	}
	if !exists {
		// 自动创建 bucket（私有）
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("创建 bucket 失败: %w", err)
		}
		fmt.Printf("[storage] 已自动创建 bucket: %s\n", cfg.Bucket)
	}

	fmt.Println("[storage] MinIO 连接成功")
	MinioClient = client
	return nil
}
