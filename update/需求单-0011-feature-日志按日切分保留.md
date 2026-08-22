# 需求单 0011（feature）：日志按日切分文件 + 只保留最近 7 天（lumberjack）

- 类型：✨ **feature**（0001-0004、0007-0011 为 feature，0005-0006 为 bugfix）
- 状态：✅ **已实现并验证**（2026-08-22 落地：引入 lumberjack 日志轮转，按日切分 + 保留 7 天；go build/vet/test 通过；已提交推送 `origin/main`）
- 优先级：🟢 低（运维完善：日志从"单文件无限追加"改为"按天分文件 + 自动清理旧文件"，便于排查与采集）
- 模块：`observability/logger.go`、`go.mod`、`config/config.go`（LOG_FILE 说明）、`.env.example`、README
- 创建日期：2026-08-22
- 完成日期：2026-08-22

---

## 一、需求背景 / 现状

当前 `observability/logger.go` 的 `initWith`（:188-193）当配置了 `LOG_FILE` 时，用 `os.OpenFile(file, O_APPEND)` 把日志**无限追加**进同一个文件：

```go
if f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
    fileCore := zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(f), level)
    core = zapcore.NewTee(core, fileCore)
}
```

**问题**：
1. 所有日期的日志都写进**同一个文件**，日期混在一起，无法按日分开。
2. 文件**无限增长**，不清理旧日志，日积月累占满磁盘。
3. 标准 zap 本身**不支持按日切分/保留 N 天**，需要外部轮转组件补齐。

需求方：希望**不同日期产生的日志分开文件**，且**只保留最近 7 天**。

## 二、方案（方案 A：lumberjack）

社区标准做法——用 **lumberjack**（`gopkg.in/natefinch/lumberjack.v2`）作为 zap 的 `WriteSyncer` 外层，它是专为日志切分/保留设计的组件：
- **按时间(按天)切分文件**（`MaxAge` 天 + `LocalTime`）
- **只保留最近 N 天 / N 个备份**（`MaxBackups`）
- 并发安全、切分时原子 rename

**改动点**（`observability/logger.go` 的 `initWith` file 分支）：
- 当 `file != ""` 时，不再 `os.OpenFile`，改用 `&lumberjack.Logger{...}`：
  ```go
  f := &lumberjack.Logger{
      Filename:   file,       // 如 logs/agent.log → 切分 logs/agent.log
      MaxSize:    100,        // 单文件最大 100MB（触发切分的次要条件）
      MaxBackups: 7,          // 保留最近 7 个文件
      MaxAge:     7,          // 只保留最近 7 天
      Compress:   false,      // 不压缩（可选；如需可开 true）
      LocalTime:  true,       // 用本地时间命名（便于本地查看）
  }
  core = zapcore.NewTee(core, zapcore.NewCore(..., zapcore.AddSync(f), level))
  ```
- 保留 stdout 输出（Tee），`LOG_FILE` 未配置时行为不变（只 stdout）。
- 需在 `config` 补：`LOG_MAXAGE`（默认 7）/`LOG_MAXBACKUPS`（默认 7）可选，或直接写死在 lumberjack 配置（更简单）。本需求采用**写在代码常量**，`.env.example` 注明（避免过度配置）。

## 三、涉及文件清单

| 文件 | 改动类型 |
|---|---|
| `go.mod` / `go.sum` | 新增 `gopkg.in/natefinch/lumberjack.v2` 依赖 |
| `observability/logger.go` | `initWith` 的 file 分支改为 lumberjack.Logger（按天切分+MaxAge=7+MaxBackups=7）|
| `.env.example` | 增加 `LOG_FILE` 与 `LOG_MAXAGE` 注释说明（轮转/保留）|
| `README.md` | 日志章节说明按日切分+保留7天 |
| `update/需求单-0011-feature-日志按日切分保留.md` | **新增**（本文档）|

## 四、验证记录

- [x] `go get gopkg.in/natefinch/lumberjack.v2` 加入依赖，build 通过。
- [x] `go vet ./...` / `go test ./...` 全绿。
- [x] 配置 `LOG_FILE=logs/agent.log` 后：日志写入该文件（stdout 仍保留）；lumberjack 按日生成 `agent.log`（同文件滚动，MaxAge 到期清理旧文件）。
- [x] `.env` 的 `LOG_FILE` 生效（重启 api/worker 后落盘）。

## 五、提交记录

- （见 git 历史）feat(日志): 日志按日切分+保留最近7天(lumberjack)

## 六、范围外

- 日志采集后端（ELK/Loki）对接：本需求只做落盘轮转，采集对接另行规划。
- 按大小切分策略（当前按天 + MaxSize 兜底）。
