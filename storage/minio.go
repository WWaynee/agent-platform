package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"

	"agent-platform/config"
)

// OSSClient 全局阿里云 OSS 客户端，业务代码通过本包的封装方法使用。
var OSSClient *oss.Client

// InitMinIO 初始化对象存储客户端（需求单 0010：MinIO → 阿里云 OSS）。
// 函数名沿用历史 InitMinIO，实际对接阿里云 OSS：从 config.O 读取 OSS 配置，
// 用静态 AccessKey 凭证 + region + endpoint 初始化客户端，并确保 bucket 存在。
func InitMinIO() error {
	cfg := config.GlobalConfig.OSS

	// 校验必填配置
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return fmt.Errorf("OSS 未配置 AccessKeyID/AccessKeySecret（检查 .env 的 OSS_ACCESS_KEY_*）")
	}
	if cfg.Bucket == "" {
		return fmt.Errorf("OSS 未配置 Bucket")
	}

	// 显式静态凭证 + 地域 + endpoint
	provider := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret)
	clientCfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(provider).
		WithRegion(cfg.Region).
		WithEndpoint(cfg.Endpoint)

	client := oss.NewClient(clientCfg)
	ctx := context.Background()

	// 检查 bucket 是否存在，不存在则自动创建（私有）
	exists, err := client.IsBucketExist(ctx, cfg.Bucket)
	if err != nil {
		return fmt.Errorf("检查 OSS bucket 是否存在失败: %w", err)
	}
	if !exists {
		if _, cerr := client.PutBucket(ctx, &oss.PutBucketRequest{Bucket: oss.Ptr(cfg.Bucket)}); cerr != nil {
			return fmt.Errorf("创建 OSS bucket 失败: %w", cerr)
		}
		fmt.Printf("[storage] 已自动创建 OSS bucket: %s\n", cfg.Bucket)
	}

	fmt.Println("[storage] OSS 连接成功")
	OSSClient = client
	return nil
}

// ============ 封装的基础操作（业务代码直接调用，屏蔽 OSS API 细节） ============

// UploadFile 上传文件到默认 bucket（OSS PutObject）
// objectKey: 对象在 bucket 内的存储路径（如 "documents/123/xxx.txt"）
// reader:    文件内容流
// size:      文件字节大小（本封装不强制，OSS 无需显式 size）
func UploadFile(objectKey string, reader io.Reader, size int64) error {
	if OSSClient == nil {
		return fmt.Errorf("OSS 客户端未初始化")
	}
	ctx := context.Background()
	_, err := OSSClient.PutObject(ctx, &oss.PutObjectRequest{
		Bucket: oss.Ptr(getBucket()),
		Key:    oss.Ptr(objectKey),
		Body:   reader,
	})
	if err != nil {
		return fmt.Errorf("上传文件到 OSS 失败: %w", err)
	}
	return nil
}

// DownloadFile 从 OSS 下载对象内容，返回其字节流（供向量化/读全文使用）。
func DownloadFile(objectKey string) ([]byte, error) {
	if OSSClient == nil {
		return nil, fmt.Errorf("OSS 客户端未初始化")
	}
	ctx := context.Background()
	res, err := OSSClient.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(getBucket()),
		Key:    oss.Ptr(objectKey),
	})
	if err != nil {
		return nil, fmt.Errorf("打开 OSS 对象失败: %w", err)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 OSS 对象内容失败: %w", err)
	}
	return data, nil
}

// DeleteFile 从默认 bucket 删除对象（OSS DeleteObject）
func DeleteFile(objectKey string) error {
	if OSSClient == nil {
		return fmt.Errorf("OSS 客户端未初始化")
	}
	ctx := context.Background()
	_, err := OSSClient.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(getBucket()),
		Key:    oss.Ptr(objectKey),
	})
	if err != nil {
		return fmt.Errorf("删除 OSS 对象失败: %w", err)
	}
	return nil
}

// GetFileURL 生成对象读写权限的预签名 URL（默认 1 小时）。
// 别名 PresignURL(objectKey, time.Hour)；供文档下载/预览（签名 URL 直链）。
func GetFileURL(objectKey string) (string, error) {
	return PresignURL(objectKey, time.Hour)
}

// PresignURL 生成指定过期时长的预签名 Get URL，浏览器可直连 OSS 下载/预览。
// objectKey: 对象存储路径；expiry: URL 有效期（如 1 小时，仅 support 到未来时间）。
func PresignURL(objectKey string, expiry time.Duration) (string, error) {
	if OSSClient == nil {
		return "", fmt.Errorf("OSS 客户端未初始化")
	}
	ctx := context.Background()
	expiration := time.Now().Add(expiry)
	result, err := OSSClient.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(getBucket()),
		Key:    oss.Ptr(objectKey),
	}, oss.PresignExpiration(expiration))
	if err != nil {
		return "", fmt.Errorf("生成 OSS 签名 URL 失败: %w", err)
	}
	return result.URL, nil
}

// getBucket 返回默认 bucket 名（从 OSS 配置读取，集中管理）。
func getBucket() string {
	return config.GlobalConfig.OSS.Bucket
}
