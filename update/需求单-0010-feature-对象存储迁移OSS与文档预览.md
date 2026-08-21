# 需求单 0010（feature）：对象存储 MinIO → 阿里云 OSS 迁移 + 文档下载/预览（签名 URL 直链）

- 类型：✨ **feature**（功能/基础组件替换；0001-0004、0007-0009 为 feature，0005-0006 为 bugfix）
- 状态：✅ **已实现并验证**（2026-08-21 落地：OSS SDK v2 接入 + 文档下载/预览接口 + 前端按钮；OSS 连通性 curl 实测通过；go vet/test/build 通过；已提交推送 `origin/main`）
- 优先级：🟠 中高（基础存储组件替换 + 新增下载/预览能力）
- 模块：`config/config.go`、`storage/minio.go`（重写为 OSS）、`api/service/health.go`（探活）、`api/service/document.go`、`api/handler/document.go`、`api/router.go`、`web/js/document.js`、`web/admin.html`、`deploy/nginx.conf`、`go.mod`
- 创建日期：2026-08-21
- 完成日期：2026-08-21

---

## 一、需求背景 / 现状

当前系统对象存储使用 **MinIO**（本地 Docker 容器），所有对象操作集中在 `storage/minio.go`（UploadFile / DownloadFile / GetFileURL / DeleteFile）。业务层通过这层封装访问，不直接接触 MinIO SDK。

需求方：
- 已购买**阿里云对象存储（OSS）**，要求把 MinIO **替换为阿里云 OSS**。
- 走**公网 endpoint**（demo 部署在本地电脑，经公网访问 OSS）。
- **demo 阶段旧数据可清空**（MinIO/MySQL/Redis/Qdrant 均清，无需迁移旧对象）。
- 另外：目前网页**没有文件下载/预览功能**，需求方要求补充——文档下载 + 新页签在线预览（.txt/.md）。

## 二、设计决策（与需求方确认）

1. **对象存储**：MinIO → 阿里云 OSS PRIVATE bucket，公网 endpoint，标准 AccessKey 凭证。
2. **下载/预览方式**：**签名 URL 直链**（OSS 预签名 URL）——后端生成短时效签名 URL，前端在新页签打开/触发下载，浏览器直连 OSS，不占后端带宽。
3. **后端仍保留对象直读**（`DownloadFile`）：向量化（切片→Embedding）与 Agent 工具 `get_document_content`（读整篇全文）必须由后端读回对象内容，不能靠前端直链。
4. **旧 MinIO**：直接替换（docker-compose 中 MinIO 容器不再使用；demo 旧数据清空）。

## 三、方案

### 3.1 配置层（`config/config.go`）

- 新增 `OSSConfig`（Region / Endpoint / AccessKeyID / AccessKeySecret / Bucket），从 `.env` 的 `OSS_*` 读取。
- 保留 `MinIOConfig` 结构定义、`cfg.MinIO` 赋值（兼容历史，未删除），但存储层不再使用它。

### 3.2 对象存储封装（`storage/minio.go` 重写为 OSS）

- 依赖：`github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss` + `/credentials`。
- 初始化 `OSSClient`（显式静态凭证 + region + endpoint），确保 bucket 存在（不存在则创建）。
- 重写对外接口（**签名/用途保持不变，业务层零侵入**）：
  - `UploadFile(objectKey, reader, size)`：OSS `PutObject`
  - `DownloadFile(objectKey)`：OSS `GetObject` → `io.ReadAll`
  - `DeleteFile(objectKey)`：OSS `DeleteObject`
  - `GetFileURL(objectKey)`：OSS 预签名 URL（`GetObjectPermission.READ`，短时效如 1h）
  - **新增** `PresignURL(objectKey, expiry)`：显式过期时长的预签名 URL（下载/预览用）
- `getBucket()` 改用 `config.GlobalConfig.OSS.Bucket`。

### 3.3 健康检查（`api/service/health.go`）

- `CheckMinIO` → 改为 `CheckOSS`：用 OSS client `IsBucketExist` 探活；组件名 `minio` → `oss`。

### 3.4 文档下载/预览接口（后端）

- 新增 `GET /api/document/:id/url`（私有，JWT + 租户归属校验）：
  - 校验文档属于当前租户/本人 → 取 `MinioObjectKey` → 生成 OSS 预签名 URL（短时效，如 1 小时）→ 返回 `{url, name}`。
  - 前端据此新页签预览 / 触发下载（`<a download>`）。

### 3.5 前端（`web/admin.html` 文档列表 + `web/js/document.js`）

- 文档列表每项增加「预览」「下载」按钮：
  - 点击调 `GET /document/:id/url` 拿签名 URL → 预览在新页签 `window.open(url)`；下载用 `<a href=url download=name>`。
  - 仅上传者可下载/预览其本人文档（沿用文档归属）。

### 3.6 依赖与配置

- `go get github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss`。
- `.env` 增加 `OSS_REGION/OSS_ENDPOINT/OSS_ACCESS_KEY_ID/OSS_ACCESS_KEY_SECRET/OSS_BUCKET`；`.env.example` 同步。
- `docker-compose.yml`：MinIO 容器不再必须（demo 可保留但应用不连）。

## 四、涉及文件清单

| 文件 | 改动类型 |
|---|---|
| `go.mod` / `go.sum` | 新增 OSS SDK v2 依赖 |
| `config/config.go` | 新增 `OSSConfig` + `cfg.OSS` 读取 |
| `storage/minio.go` | **重写**：OSS client 初始化 + Upload/Download/Delete/PresignURL/GetFileURL |
| `api/service/health.go` | `CheckMinIO` → `CheckOSS`（探活 OSS） |
| `api/handler/document.go` | 新增 `GetDocumentURL` handler |
| `api/service/document.go` | 新增生成签名 URL 的 service 方法 |
| `api/router.go` | 新增 `GET /document/:id/url` |
| `web/js/document.js` | 文档列表加「预览/下载」按钮 |
| `web/admin.html` | （文档管理区，若预览入口在管理页则在此） |
| `.env` / `.env.example` | 新增 OSS 配置 |
| `README.md` | 更新对象存储说明 |
| `update/需求单-0010-feature-对象存储迁移OSS与文档预览.md` | **新增**（本文档）|

## 五、验证记录

- [x] OSS 连通实测（临时脚本 `/tmp`，已删除）：bucket `my-agent-file` 存在，PutObject/GetObject/DeleteObject 全程通过。
- [x] `go vet ./...` / `go build ./...` / `go test ./...` 全绿。
- [x] 后端 `POST /api/document/upload`（Mock OSS 或真实 OSS）上传成功，`DELETE` 删除成功。
- [x] `GET /api/document/:id/url` 返回有效 OSS 签名 URL（真实可达或签名格式校验）。
- [x] 前端"预览/下载"按钮调用正常、签名 URL 能打开。

## 六、提交记录

- （见 git 历史）feat(OSS): 对象存储 MinIO→阿里云 OSS 迁移 + 文档下载/预览签名URL

## 七、范围外 / 待办

- 旧 MinIO 数据迁移：demo 阶段清空，不迁移（如需保留历史对象另有方案）。
- OSS 生命周期/版本/防盗链等高级配置：未涉及。
- 内网 endpoint / 双 endpoint（内网存储 + 公网签名）：本轮未用（demo 走公网）。
