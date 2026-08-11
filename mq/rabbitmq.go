package mq

import (
	"context"
	"errors"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"agent-platform/config"
)

// ============ RabbitMQ 基础客户端封装 ============
//
// 作用：把 RabbitMQ 连接/Channel/队列声明 收敛到本包，供异步任务
// （文档解析后台处理）生产/消费消息复用同一个连接。
//
// 与 storage/redis.go 同样的收敛思路：初始化一次、赋值给全局变量、
// 业务代码直接使用，避免到处 new 连接。
//
// 为什么手动 ACK（消费者成功才确认）：
//   自动 ACK（autoAck=true）时，消息一取出就算"已处理"，若消费者在处理
//   过程中崩溃，这条消息就丢了。
//   手动 ACK 下：处理成功才 Acknowledge；失败则 Nack(requeue=true) 让消息
//   重新入队，下次还能再处理 —— 保证消息不丢。

// MQ 全局连接与 Channel，业务代码直接使用
// ⚠️ 使用前必须已调用 InitRabbitMQ 成功，否则为空指针 panic。
var (
	MQConn *amqp.Connection
	MQCh   *amqp.Channel
)

// InitRabbitMQ 初始化 RabbitMQ 连接、Channel，并声明队列（不存在则自动创建）。
//
// 读取 config.GlobalConfig.RabbitMQ（Host/Port/Username/Password/Vhost/QueueName）。
// 流程：
//  1. 用配置拼 AMQP DSN 建立连接（amqp.Dial）——地址不可达 / 账号密码错误会在此暴露；
//  2. 创建 Channel；
//  3. 声明队列（durable=true 持久化，RabbitMQ 重启/Restart 消息不丢）；
//  4. 赋值给全局 MQConn / MQCh。
//
// ⚠️ 启动流程必须调用（返回错误即 main 应停止）：不初始化会导致生产/消费空指针 panic。
func InitRabbitMQ() error {
	cfg := config.GlobalConfig.RabbitMQ

	// 1. 拼 AMQP DSN：amqp://user:pass@host:port/vhost
	dsn := fmt.Sprintf("amqp://%s:%s@%s:%d/%s",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, vhostPath(cfg.Vhost))

	conn, err := amqp.Dial(dsn)
	if err != nil {
		return fmt.Errorf("连接 RabbitMQ 失败（%s:%d vhost=%s）: %w", cfg.Host, cfg.Port, cfg.Vhost, err)
	}

	// 2. 创建 Channel
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("创建 RabbitMQ Channel 失败: %w", err)
	}

	// 3. 声明文档解析队列（durable=true，RabbitMQ 重启消息不丢；不存在自动创建）
	if _, err := ch.QueueDeclare(cfg.QueueName, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return fmt.Errorf("声明队列 %q 失败: %w", cfg.QueueName, err)
	}

	// 4. 赋值给全局，供业务代码使用
	MQConn = conn
	MQCh = ch
	return nil
}

// vhostPath 把 vhost 处理成 URL path 片段。
// RabbitMQ 默认 vhost 是 "/"，在 DSN 里表示为空（amqp://host:port/ 即根 vhost）或 %2f。
// 这里对 "/" 特判返回空串，其余 vhost 原样拼接。
func vhostPath(vhost string) string {
	if vhost == "" || vhost == "/" || vhost == "%2f" {
		return "" // 默认 vhost，DSN 尾部留空
	}
	return vhost
}

// Publish 把一条消息发布到指定队列。
//
// 参数：
//   - queueName：目标队列名
//   - body：消息体（二进制）
//
// 消息属性：
//   - DeliveryMode=Persistent（对应 durable 队列 + 持久化消息）：写入磁盘，
//     RabbitMQ 宕机/重启后消息仍可恢复，不丢。
//   - 默认投递到队列本身（exchange 为空串，routing key = queueName）。
func Publish(queueName string, body []byte) error {
	if MQCh == nil {
		return fmt.Errorf("RabbitMQ 未初始化，请先调用 InitRabbitMQ")
	}
	return MQCh.PublishWithContext(
		context.Background(),
		"",        // exchange 为空 = 默认交换机，按队列名投递
		queueName, // routing key = 队列名
		false,     // mandatory
		false,     // immediate
		amqp.Publishing{
			ContentType:  "application/octet-stream",
			DeliveryMode: amqp.Persistent, // 消息持久化，重启不丢
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
}

// ErrRequeue 哨兵错误：handler 返回的错误若包装了它（errors.Is(err, ErrRequeue) 为真），
// 表示这条消息需要**重新入队**再试（Nack requeue=true）；否则一律 ACK 确认。
//
// 为什么区分"重试"与"最终失败"：
//   - 像依赖的外部服务瞬时故障（LLM/Qdrant 抖动），值得重新入队重试；
//   - 而业务性最终失败（如文档不存在、内容为空）无需无限重试，失败即 ACK 丢弃，
//     避免消息在队列里无限循环消耗资源。
var ErrRequeue = errors.New("mq: requeue")

// Consume 注册一个消费函数，持续消费指定队列，并把每个消息投递到 handler。
//
// 参数：
//   - queueName：队列名
//   - handler：处理函数，返回 error。
//   - 返回 nil → 成功，Ack（消息移除）；
//   - 返回包装了 ErrRequeue 的错误 → 需要重试，Nack(requeue=true) 重新入队；
//   - 返回其他 error → 最终失败，也 Ack（确认丢弃，避免无限重复消费）；
//   - handler 内部 panic → 也被捕获，视为最终失败 Ack，不让单条消息拖垮整个 worker。
//
// 手动 ACK + 失败即确认的设计（保证不无限循环）：
//
//	自动 ACK 会丢消息；而"失败一律 Nack requeue"又会让失败消息无限循环。
//	本项目取折衷：只有显式声明需要重试（ErrRequeue）才重新入队，其余失败 Ack，
//	既尽可能不丢重要任务，又避免死循环耗尽资源。
//
// ⚠️ 阻塞调用：本方法会持续消费，应在协程 goroutine 里运行。
func Consume(queueName string, handler func([]byte) error) error {
	if MQCh == nil {
		return fmt.Errorf("RabbitMQ 未初始化，请先调用 InitRabbitMQ")
	}

	// autoAck=false：手动 ACK
	deliveries, err := MQCh.Consume(queueName, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("创建消费器失败（queue=%s）: %w", queueName, err)
	}

	for d := range deliveries {
		// 单个消息的处理（含 panic 捕获），返回未经包装的判断结果
		processErr := safeProcess(handler, d.Body)

		switch {
		case processErr == nil:
			// 成功：确认，消息移除
			_ = d.Ack(false)
		case errors.Is(processErr, ErrRequeue):
			// 需要重试：不确认，消息重新入队，之后还能再消费
			_ = d.Nack(false, true) // multiple=false, requeue=true
		default:
			// 最终失败（或 panic 转成的错误）：也 Ack 确认丢弃，避免无限重复消费
			_ = d.Ack(false)
		}
	}
	return nil
}

// safeProcess 安全执行一次消息处理：捕获 handler 内部 panic，转成 error。
// 这样某条消息如果 panic，不会向上传播把整个消费者协程打崩。
func safeProcess(handler func([]byte) error, body []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("消费处理 panic: %v", r)
		}
	}()
	return handler(body)
}
