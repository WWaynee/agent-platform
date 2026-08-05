package storage

import (
	"context"
	"fmt"
	"io"
	"time"

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

// ============ 封装的基础操作（业务代码直接调用，屏蔽 MinIO API 细节） ============

// UploadFile 上传文件到默认 bucket
// objectKey: 对象在 bucket 内的存储路径（如 "documents/123/xxx.pdf"）
// reader:    文件内容流
// size:      文件字节大小
// 后续若切换其他对象存储（如阿里云 OSS），只需改本层实现，业务代码不变
func UploadFile(objectKey string, reader io.Reader, size int64) error {
	ctx := context.Background()
	_, err := MinioClient.PutObject(ctx, getBucket(), objectKey, reader, size, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("上传文件失败: %w", err)
	}
	return nil
}

// GetFileURL 获取对象的临时签名访问 URL
// objectKey: 对象存储路径
// 返回的 URL 在有效期内（默认 7 天）可直接用于下载
func GetFileURL(objectKey string) (string, error) {
	ctx := context.Background()
	url, err := MinioClient.PresignedGetObject(ctx, getBucket(), objectKey, 7*24*time.Hour, nil)
	if err != nil {
		return "", fmt.Errorf("生成文件访问地址失败: %w", err)
	}
	return url.String(), nil
}

// DeleteFile 从默认 bucket 删除对象
func DeleteFile(objectKey string) error {
	ctx := context.Background()
	err := MinioClient.RemoveObject(ctx, getBucket(), objectKey, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("删除文件失败: %w", err)
	}
	return nil
}

// getBucket 返回默认 bucket 名（从配置读取，集中管理，避免各方法重复取）
func getBucket() string {
	return config.GlobalConfig.MinIO.Bucket
}
