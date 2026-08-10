# 多租户智能 Agent 工作台

面向企业的 SaaS 智能助手平台。每家企业作为独立租户，上传内部文档搭建专属知识库；员工通过自然语言对话提问，智能 Agent 自主检索知识库、调用授权工具，给出精准回答。

## ✨ 用户能做什么

### 租户管理员

- 独立管理企业空间，与其他企业数据完全隔离
- 上传文档，自动构建企业私有知识库
- 管理已上传文档，删除失效资料
- 控制员工可使用的工具权限
- 查看平台调用消耗、使用频次

### 普通员工

- 自然语言提问企业内部问题
- AI 自动检索本企业知识库辅助回答
- 支持多轮连续对话，AI 保留上下文
- 大型文档后台异步处理，无需等待

## 📅 两周开发计划

### 第一周

#### 周一・环境搭建 & 数据表设计

- [x] 初始化 Go 项目，建立目录结构
- [x] 配置 `.gitignore`，屏蔽密钥和编译产物
- [x] 编写 `docker-compose`，启动 MySQL/Redis/MinIO/Qdrant
- [x] 验证四个中间件全部正常连接
- [x] MinIO 创建 `document-store` bucket
- [x] 设计 7 张核心数据表
- [x] 编写 GORM 模型结构体
- [x] 实现 migrate 迁移工具
- [x] 执行迁移，确认 7 张表生成
- [x] 完成 `docs/db.md` 数据表设计文档

#### 周二・HTTP 服务底座 & 鉴权

- [x] 安装 Gin 依赖
- [x] 安装 bcrypt、JWT 依赖（用户注册 / 登录阶段）
- [x] 建立 config 包，统一加载 `.env` 配置
- [x] 实现统一返回格式工具
- [x] 实现全局异常恢复中间件
- [x] 编写 `main.go` 入口，启动 Gin 服务
- [x] 路由分层，公开路由 / 私有路由分组
- [x] `/health` 健康检查接口调通
- [x] 实现租户创建接口
- [x] 实现租户列表 / 详情接口
- [x] 实现用户注册接口，密码 bcrypt 加密
- [x] 实现用户登录接口，返回 JWT
- [x] JWT 鉴权中间件
- [x] 从 JWT 解析 `tenant_id` 注入上下文
- [x] 测试：无 token 访问返回 401
- [x] 测试：带 token 能从上下文拿到租户信息

#### 📌 周二遇到的问题与解决方案

1. **联合唯一索引只建在 username 单列**：GORM 里 `uniqueIndex:idx_tenant_user` 只标在 `Username` 字段，`TenantID` 未挂同名索引，导致不同租户无法有同名用户，违反多租户设计。
   → 修复：数据库删除旧单列索引，重建 `(tenant_id, username)` 联合唯一索引；`TenantID` 挂同名 `uniqueIndex`。实测租户 A/B 可共存同名 admin。

2. **JWT 载荷不够最小化**：`GenerateToken` 误把 `username` 塞进 token，违背"JWT 只放鉴权最小字段"原则，且 username 是可变信息、不参与鉴权。
   → 修复：载荷收敛为 `user_id / tenant_id / role` 三者，登录接口返回体单独带 user 对象供前端展示。

3. **鉴权失败 HTTP 状态码不一致**：`response.Unauthorized` 用 `Fail()` 固定返回 HTTP 200（仅 body 的 code=401），与登录错误密码返回的 HTTP 401 不一致，前端拦截器无法靠状态码判断跳登录。
   → 修复：`Unauthorized` 改用 `FailStatus` 返回真正的 HTTP 401（方案 C），一劳永逸。

#### 周三・文件上传 & 文档管理

- [x] 封装 MinIO SDK 工具类（UploadFile / GetFileURL / DeleteFile）
- [x] 初始化 MinIO 客户端（InitMinIO，检查/创建 bucket）
- [x] 实现文件上传接口（POST /api/document/upload）
- [x] 上传文件存入 MinIO（objectKey 带 tenant_id 前缀 + timestamp 防覆盖）
- [x] 文档元信息写入 document 表（含 user_id 上传者，status 先置 pending）
- [x] 实现文档列表查询接口（GET /api/document/list，分页）
- [x] 实现文档详情接口（GET /api/document/:id）
- [x] 实现文档删除接口（DELETE /api/document/:id，MinIO + DB 同步删）
- [x] 测试：上传文件，MinIO 有文件，DB 有记录
- [x] 测试：只能查当前租户的文档（多租户隔离）
- [x] 测试：租户 B 看不到 / 删不掉租户 A 的文档
- [x] 测试：所有文档接口不带 token 返回 401
- [x] 测试：所有查询强制 tenant_id 过滤，无越权漏洞

#### 📌 周三遇到的问题与解决方案

1. **document 表缺「上传者」字段**：上传接口要记录"谁上传的文档"，但 `documents` 表最初只有 `tenant_id / name / minio_object_key / status / size`，无法落 uploader。
   → 修复：`Document` 模型新增 `UserID uint64` + 索引，跑 AutoMigrate 加列，service 层把从 JWT 拿到的 `userID` 写入 `UserID`，并同步更新 `docs/db.md`。

2. **bucket 不一定存在**：`InitMinIO` 初始化客户端时，`document-store` bucket 可能尚未创建（启动时第一次连接成功但桶不存在），直接上传会报 Bucket not found。
   → 修复：`InitMinIO` 里 `BucketExists` 判断，不存在则 `MakeBucket` 自动创建（兜底），避免依赖手工建桶。

3. **GORM 表名是复数、易用错**：查询时容易写成 `SHOW COLUMNS FROM document`，实际 GORM 默认表名是复数 `documents`，导致 `Table doesn't exist`。
   → 修复：确认 GORM 复数表名约定，所有 SQL/模型 Debug 均用 `documents`；验证时用 `SHOW TABLES` 确认真实表名。

4. **多租户越权风险（最关键）**：详情/删除若只按 `id` 查询，租户 B 可猜到租户 A 的文档 ID 并查看或删除。
   → 修复：storage 层所有查询强制 `WHERE id=? AND tenant_id=?`（列表 `WHERE tenant_id=?`）；service 层将 `gorm.ErrRecordNotFound` 与"属于别的租户"统一转成「文档不存在」，**不区分"不存在"与"无权访问"**，防止被横向探测其他租户数据是否存在。

5. **上传 objectKey 只用文件名会互相覆盖**：不同用户传同名 `方案.docx` 会彼此覆盖丢数据。
   → 修复：objectKey 用 `{tenant_id}/{timestamp}_{filename}`，tenant_id 前缀实现 MinIO 内按租户分目录、timestamp 保证唯一，同名文件不再冲突。

6. **删除需 MySQL 与 MinIO 双删**：只删 DB 记录而保留 MinIO 文件会形成孤儿文件、浪费存储。
   → 修复：`service.DeleteDocument` 先查归属（带租户过滤）→ `DeleteFile` 删 MinIO 实际文件 → 软删 DB 记录，两处数据保持一致。

#### 周四・LLM 客户端封装

- [x] 封装 LLM HTTP 请求客户端
- [x] 实现 Chat 对话接口调用
- [x] 实现 Embedding 向量生成接口
- [x] 超时控制
- [x] 指数退避重试机制（1s→2s→4s + 抖动；500 重试 3 次 / 401 不重试 / 超时会重试）
- [x] 简易熔断器（Closed→Open→Half-Open 三态流转；Open 直接拒绝不发 HTTP）
- [x] 结构化输出校验（ChatWithJSON：严格 JSON 提示 + 简单修复 + 重试一次）
- [x] Token 用量统计封装（内置累计统计 + 用量回调钩子，供租户统计/限流预留）
- [x] 测试：正常对话请求返回结果
- [x] 测试：Embedding 调用返回向量（实测硅基流动，4096 维）
- [x] 测试：模拟超时，触发重试（单元测试：1ms 超时访问慢端点，快速失败不卡死）

#### 📌 周四遇到的问题与解决方案

1. **DeepSeek 不开放 Embedding 接口**：官方向量接口返回 404，原配模型也不存在，向量功能无法落地。
   → 解决：向量服务改接**硅基流动**，实测返回 4096 维向量、token 用量正常。

2. **Chat 与 Embedding 分属不同厂商（两套 key + 域名）**：对话走 DeepSeek、向量走硅基流动，key 和域名都不同，共享一套配置会冲突。
   → 解决：向量支持**独立配置、留空自动回退主配置**，实现多厂商又能向后兼容单一厂商场景，业务侧零改动。

3. **不同厂商 baseURL 是否带 `/v1` 前缀不一致**：DeepSeek 不带、硅基流动带，拼接方式不同。
   → 解决：baseURL 由各厂商在配置里各自指定，代码统一按配置拼接接口路径，互不影响。

4. **超时 + 重试的组合陷阱（容错·超时）**：超时只作用于单次请求，一旦进入重试循环会因"整体无时间预算"而反复超时重试，把请求无限拖住，违背快速失败。
   → 解决：为整轮请求（含所有重试）设**整体时间预算**，预算耗尽即立即返回超时并停止重试。实测 1ms 超时访问慢端点仅约 1ms 即快速返回、不卡死、服务稳定。
   ⚠️ 注意点：重试只对**瞬时/服务端**错误有意义，超时必须受整体预算约束，否则会被重试无限放大。

5. **指数退避重试的标准化（容错·重试）**：最初的退避是非标准固定基数且无抖动，重试错误语义也不统一。
   → 解决：采用标准**指数退避 + 随机抖动**（1s→2s→4s，抖动用于多客户端错开重试时机、防雪崩）；只对网络错误/超时/服务端 5xx 重试，调用方错误（4xx）不重试；429 限流本次按"不重试"处理（方案A，后续再按限流语义接入慢退避）。
   ⚠️ 注意点：自测为了毫秒级复现指数趋势，测试中把小基数注入客户端；生产默认仍是 1s + 20% 抖动。

6. **简易熔断器（容错·熔断）**：仅靠超时与重试，服务持续故障时仍会反复请求打垮下游、浪费资源，需要状态层面的整体短路。
   → 解决：实现**三态熔断器（Closed → Open → Half-Open）**，客户端内部生效、业务无感知。窗口内失败率超标（且请求数够多）即熔断打开——此后请求**直接快速失败、不再发 HTTP**；熔断持续一段时间后进入半开放一个试探请求：成功回关闭、失败继续熔断。参数全部可配，便于测试快速验证。
   ⚠️ 注意点：熔断与重试需协作有序——熔断期间的错误必须**不可重试**，否则 Open 时仍会重试打垮下游，熔断就失去意义。

7. **结构化 JSON 输出校验与修复（Agent 前置）**：周六 ReAct 需要 LLM 返回结构化 Action（调哪个工具、传什么参数），格式不对整条流程就崩；而 LLM 常会包代码块、加前后缀文字、截断括号，这是 Agent 开发最常见的坑。
   → 解决：提供**"必须返回严格 JSON + 简单修复 + 重试一次"**三步容错：先注入严格 JSON 提示；拿到响应先尝试解析，失败则自动剥离代码块、取对象主体、补全括号；仍失败则明确告知"只返回 JSON"再请求一次，再不行返回含原始内容的格式错误。
   ⚠️ 注意点：只对**格式问题**触发"重试改格式"，网络/超时/熔断等上游错误直接返回、绝不误触；调用方需预先定义好期望的 JSON 结构，本能力保证"能解析"，不保证结构完全匹配。

8. **Token 用量统计封装（为租户统计/限流预留）**：每次调用的 token 用量可用但没有统一采集点，且下周要做租户用量统计与限流，届时再补就要到处改客户端。
   → 解决：客户端内置**用量统计封装**，两块能力：① **累计用量统计**，并发安全累加调用次数与各类 token 总量，可随时查看整体消耗；② **用量回调钩子接口**，每次调用（成功或失败）都上报一次操作、模型、用量、成败、耗时，供上层做租户统计/限流，面向接口可替换、可扩展。为租户维度预留：回调携带发起调用的上下文，上层注入租户标识即可，无需改动调用接口。
   ⚠️ 注意点：回调应**快速返回/异步消费**，避免阻塞业务调用；失败也计入调用次数，但不增加 token 消耗。

#### 周五・Agent 骨架搭建

- [x] agent 包分层：engine / memory / toolmanager
- [x] 定义工具标准接口
- [x] 实现工具注册机制
- [x] 预留工具权限校验（PermissionChecker 可注入钩子）
- [x] 记忆接口 Memory + 内存版 InMemoryMemory（周日会话记忆前置，暂接纯内存）
- [x] 定义 ReAct 引擎结构体
- [x] 定义 Run 方法签名与 ReAct 流程注释（主循环周六实现）
- [x] 定义上下文结构体
- [x] 定义 Agent 输入输出结构体
- [x] 代码编译通过，依赖结构清晰
- [x] 注册一个空测试工具验证流程

#### 周六・RAG 链路 & ReAct 引擎【攻坚日】

- [x] 实现文档文本切片逻辑
- [x] 切片后调用 Embedding 生成向量
- [x] 封装 Qdrant SDK，向量写入
- [x] 向量写入时携带 tenant_id 元数据
- [x] 实现向量检索接口，强制 tenant_id 过滤
- [x] 测试：上传文档 → 切片 → 向量入库
- [x] 测试：租户 A 检索不到租户 B 的文档
- [x] 实现完整 ReAct 循环调度引擎
- [x] 开发知识库检索工具
- [x] 测试：提问文档内容，Agent 自动调用 RAG
- [x] 测试：无关问题，Agent 不调用工具直接回答

#### 📌 周六遇到的问题与解决方案

1. **切片策略与重叠**：纯按字符硬切会正好在句子中间断开，相邻两端语义都不完整，检索时难以命中完整上下文。
   → 解决：`splitter` 包定义 **ChunkSize=600 / OverlapSize=80** 策略常量；`SplitText` 按「段落优先 + 超长回找分隔点 + 相邻切片保留 OverlapSize 重叠」切分，保证被切断的语义在至少一个切片里连续。用 `[]rune` 处理长度，中文不会切成半个字。

2. **Embedding 只能单条调用，切片多会非常慢**：原 `llmclient.Embed` 每次只转一条文本，文档切出几十片就得几十次 HTTP 往返。
   → 解决：新增 **`EmbedBatch`**（输入 `[]string`，OpenAI 兼容接口 input 传数组一次返回全部向量），`ProcessDocument` 以 `embedBatchSize=16` 分批批量向量化，写入也走 `UpsertVectors` 一次批量 upsert，显著减少往返。

3. **documents 表缺 `error_msg` 列**：`UpdateDocumentResult` 想记录失败原因，但表一直没建这列，首次调用直接报 `Unknown column`。
   → 解决：迁移工具/手工 `ALTER TABLE documents ADD COLUMN error_msg TEXT` 补齐，使"失败时记录原因、成功时清空"真正落地。

4. **向量点 ID 冲突与幂等**：不同文档、不同切片若 ID 撞车会互相覆盖；重跑向量化又可能产生重复点。
   → 解决：点 ID 用 `(documentID << 32) | chunkIndex` 合成高 32 位文档号 + 低 32 位切片号 —— 跨文档必不冲突；同一文档重跑时同一切片 ID 相同 → upsert 覆盖，天然幂等。

5. **同步执行时 processing 态瞬时被覆盖**：测试接口同步调用，从 pending 直接到 success，中间 processing 态几乎不可见。
   → 解决：`ProcessDocument` 在查到文档后立即显式 `UpdateDocumentStatus("processing")`；实测用多切片长文档 + 高频轮询能捕获到 processing 态（持续约数百 ms），证明三态流转真实发生。MQ 异步化后该态供前端轮询。

6. **MinIO 缺「下载读回」方法**：原封装只有 `UploadFile/GetFileURL/DeleteFile`，不能把对象内容读回内存。
   → 解决：新增 `DownloadFile(objectKey) ([]byte, error)`，用 `GetObject` 流式读完对象，`ReadTextDocument` 据此把 txt/md 读为 UTF-8 文本（PDF 暂不支持，返回明确错误）。

7. **向量化流程的异常处理闭环**：ProcessDocument 任一环节失败（文件/读取/Embedding/写库）都必须落 failed + error_msg，且服务不 panic。
   → 解决：所有失败步骤统一走 `failProcess`（落 failed + 记录原因）；EmbedBatch 返回数量与请求不符时防御性拦截，防止向量与切片错位。
   → 边界：**文档不存在**时直接返回哨兵错误 `ErrDocumentNotFound`，handler 用 errors.Is 识别后返回 400，**不置 failed**（文档本身不存在，置 failed 无意义且误导排查）。
   → 自测：故意传不存在文档 ID → 返回 400"文档不存在或无权访问"，服务不崩；故意把 Embedding key 写错 → 文档状态置 failed、error_msg 完整记录 API 返回的错误，服务不崩。

8. **知识库检索 service 与多租户隔离**：要把"问题 → 相关切片"的检索能力封装给上层（RAG 对话 / ReAct 工具），且 tenant_id 隔离必须硬性保证。
   → 解决：新增 `api/service/knowledge.go` 的 `Search(tenantID, query, topK) ([]SearchHit, error)`：query 先 Embedding 转向量，再调 `storage.SearchVectors`。**隔离底线在 storage 死守**——`SearchVectors` 的签名强制 `tenantID uint64` 参数（不传编译不过），函数内部用 `tenantIDFilter` 构造 `tenant_id` 等值过滤，业务层传错也被过滤兜住。
   → 自测（带 tenant_id 逐条核对命中点）：租户 9988 / 1 / 2 各检索，命中的每一条 `tenant_id` 都等于发起检索的租户，**零跨租户泄漏**。topK≤0 自动取默认 3。

9. **知识库检索测试接口（HTTP）**：`service.Search` 是内部方法，调试/联调需要一个能传 query 返回片段的 HTTP 入口。
   → 解决：新增 `POST /api/knowledge/search`（私有组，JWT 鉴权），请求 `{"query":"问题","top_k":3}`，tenant_id 从 JWT 上下文取，返回 `results[]`（content/score/document_id/chunk_index）。
   → 自测：上传多主题长文档（篮球/咖啡/海洋各成片段）→ 向量化 → 分别检索"篮球走步二次运球""咖啡豆品种拉花"→ **top 结果全部命中对应主题片段**，证明结果与问题相关；租户 1 / 9988 各自只见自己的向量点（HTTP 层隔离生效）。
   → 健壮性：空 query / 缺字段 / 非法 JSON → 400；无 token → 401，服务不崩。
   ⚠️ 结论：检索接口 + 多租户隔离链路（上传→切片→向量化→检索）全链路已打通且可调试。

10. **多租户隔离硬性验证（重中之重）**：检索/写入是租户数据隔离的生命线，必须用"租户 A 机密数据"真实对抗验证，不能只看代码。
   → 测试（全新租户，杜绝历史数据干扰）：租户 A 上传含"机密信息：123456"的文档并向量化；租户 B 上传无关普通文档并向量化；Qdrant 确认 A 点 tenant_id=A、B 点 tenant_id=B。
   → 关键判定：**用租户 B 身份搜 `123456` / `机密编号` / `绝密数据` → 完全搜不到 A 的内容**（只返回 B 自己无关键字的普通文档）；用租户 A 身份搜 `123456` → 精确命中自己的机密文档；**逆向交叉**（A 搜 B 的办公内容）也搜不到 B。
   → 双向验证通过 ✅：A 搜自己能搜到、B 搜 A 独有内容搜不到。隔离无硬伤。
   → 原理复盘：写入时 `QdrantVector.TenantID` 从 JWT 取并写进 payload；检索时 `SearchVectors` 签名强制 `tenantID` 参数，函数内部用 `tenantIDFilter` 构造 `tenant_id` 等值过滤——即使上层传错，filter 也是正确构造的，杜绝跨租户泄漏。

11. **知识库检索工具 KnowledgeRetrieveTool**：把 RAG 检索封装成 Agent 可调用的工具，供 ReAct 引擎检索企业私有知识。
   → `toolkit/knowledge_retrieve.go` 实现 `toolmanager.Tool` 接口四方法：
     - `Name()` = `knowledge_retrieve`
     - `Description()` 明确「何时用 / 参数 / 返回」：涉及内部资料、公司规定、产品说明等私有知识且需事实依据时用；返回最相关片段及其来源
     - `Parameters()` = JSON Schema（必填 `query`）
     - `Execute(ctx, params)`：解析 query → 用 `ctx.TenantID` 调 `service.Search`（隔离底线仍在 storage 层）→ 拼接片段成文本返回
   → 自测：编译期断言实现 Tool 接口；注册到 ToolManager 可查可调；单独 Execute 传 `{"query":"篮球规则"}` 返回对应知识片段；空 query / 非法 JSON 返回明确错误，不 panic。

12. **工具注册 + 工具权限校验（补周五 TODO）**：程序启动时注册 KnowledgeRetrieveTool 到 ToolManager；补上 ExecuteTool 预留的权限校验（tenant_tool_config 白名单）。
   → 启动注册：`cmd/api/main.go` 建 ToolManager → `RegisterTool(NewKnowledgeRetrieveTool())` → `SetPermissionChecker(service.NewDBPermissionChecker())`，日志确认"已注册 1 个工具: [knowledge_retrieve]"。
   → 权限校验：新增 `storage/tool_config.go`（GetToolConfig / SetToolConfig 的 upsert）+ `api/service/tool_permission.go`（`DBPermissionChecker` 实现 `toolmanager.PermissionChecker`）。规则：查到 IsEnable=false → 拒绝；查不到记录 → 知识库工具在 `DefaultEnabledTools` 里默认放行。
   → 自测：关闭租户 9988 的 knowledge_retrieve → ExecuteTool 返回"无权限"；开启 → 正常调用。
   ⚠️ **踩坑记录（bool 默认值）**：`TenantToolConfig.IsEnable` 若带 `gorm:"default:true"` 标签，会因 **bool 零值(false)被 gorm 当"未赋值"而替换成列默认值 true**，导致"关闭"操作写入后实际仍是开启、权限校验失效。已去掉该标签修复。教训：bool 开关字段的 gorm default 标签要格外小心。

13. **System Prompt 模板（ReAct 循环的灵魂）**：LLM 完全靠 Prompt 判断"要不要调工具、按什么格式调、何时直接答"，模板质量直接决定 ReAct 能否工作。
   → `agent/engine/prompt.go` 的 `BuildSystemPrompt(systemRole, tools)` 从 ToolManager 实时取工具列表动态拼装，保证"Prompt 描述的工具 == 实际可用的工具"，避免诱导 LLM 调不存在的工具。
   → 模板五大段：① 角色设定 ② 可用工具列表（Name + Description + JSONSchema）③ 输出格式要求（`{"action", "action_input"}` / `final_answer`）④ 正例 ⑤ 规则（一次一条 JSON、只能用列出的工具、不知道就说不知道勿编造）。
   → 自测：含 knowledge_retrieve/echo 工具列表、参数 Schema、action 格式、final_answer、示例、规则，全部核验通过。

14. **LLM 输出解析器（多层容错）**：LLM 不一定严格按 `{action,action_input}` 输出，解析失败会让 Agent 崩溃，故做多层容错"尽力救回"。
   → `agent/engine/parser.go`：`parseLLMOutput(output)`（导出 `ParseLLMOutput`）返回 `*ParsedOutput{Action, Input}`。
   → 容错候选按可修复程度依次尝试：① 原文字符串直接解析 ② 剥掉 ```json/``` 代码围栏 ③ 取第一个 `{` 到最后一个 `}` 截取。缺失 action 判定为非法；全失败返回错误（绝不 panic）。
   → 自测：标准 JSON ✅、```json围栏 ✅、前后多余文字 ✅、纯```围栏 ✅、完全乱→返回错误不 panic ✅、空串→错误不 panic ✅。

15. **ReAct 主循环 Run 实现（思考→行动→观察）**：把 SystemPrompt + 解析器 + ToolManager + Memory + LLM 串成完整循环。
   → `engine.go` 的 `Run(ctx, query)`：从 Memory 读历史 → 组装 system(角色+工具列表+格式)+历史+提问 → 循环调 LLM 得输出 → 解析；
      final_answer 即结束；否则记录 ToolCall 并经 `ToolManager.ExecuteTool` 执行（含租户权限校验），把结果包装成 `观察结果` 喂回上下文继续下一轮。
   → 容错：解析失败塞回纠错提示重试（最多 1 次，常量 `maxParseRetries`）；达 MaxIterations 走兜底收尾（`fallbackResponse`，不 panic，并补回 ToolCalls 审计）。
   → 每轮 `log` 打印会话/轮次/LLM输出/工具调用，便于调试。对话（user+assistant）经 `persist` 写回 Memory。
   → 自测（mock LLM 预设序列）：A 工具调用→观察→final_answer→存记忆 全链打通 ✅；B 首轮乱码解析失败→重试成功 ✅；C 达最大迭代强制收尾、ToolCalls 完整、不 panic ✅。`go test ./agent/...` 全绿。

16. **对话接口 POST /api/chat（端到端落地）**：把 ReAct 引擎暴露成 HTTP 接口，真正可调用。
   → `api/handler/chat.go` 的 `Chat` handler：私有路由组（JWT 鉴权），`tenant_id`/`user_id` 一律从 JWT 上下文拿（唯一可信来源，不信前端）。入参 `{session_id, query}` → 构造 `engine.AgentContext{TenantID,UserID,SessionID}` → `engine.Run` → 返回 answer + tool_calls。
   → 组件装配放 `cmd/api/main.go`：`llmclient.NewClient` → `engine.NewLLMAdapter`（把完整 llmclient.Client 薄封装成 engine.LLMClient 最小接口）→ `engine.NewReActEngine(llm, tm, memory.NewRedisMemory(storage.RDB), "")` → `handler.SetAgentEngine` 注入。
   → （注：最初骨架用 `InMemoryMemory` 跑通；周日第3小步已把生产装配切为 **Redis 版 RedisMemory** 使会话历史落 Redis、重启不丢。内存版 `InMemoryMemory` 仍保留，供单测 / smoke 使用。）
   → `agent/engine/llm_adapter.go`：把 engine.Message → llmclient.ChatMessage，调用后返回回复文本。
   → 自测：未带 token → 401 ✅；带 token → 返回 `answer:"1+1等于2"`、`tool_calls:[]` ✅。

17. **端到端：常识问题直接回答（不调工具）**：问 "1+1等于几?"，LLM 第1轮即 `{"action":"final_answer",...}`，日志无"调用工具"、返回 `tool_calls:[]` ✅。
   → Prompt 写得对：LLM 清楚"可直接回答时不调工具"，未出现误触发工具。

18. **端到端：知识库问题自动检索（核心成功）**：上传《员工手册》→ 向量化 → 问"公司的带薪年假是多少天？"。
   → 日志：第1轮 LLM 输出 `{"action":"knowledge_retrieve","action_input":"{\"query\":\"带薪年假天数\"}"}` → 调用工具 → 第2轮 final_answer。
   → 回答"根据员工手册第二章第3条…每年享有5天带薪年假"，与文档一致非编造；`tool_calls:["knowledge_retrieve"]` ✅。
   ⚠️ **踩坑记录（DeepSeek tool 角色协议）**：把观察结果用 `role:"tool"` 喂回会给 DeepSeek 报 HTTP 400（`missing tool_call_id`）——因为本引擎是"文本JSON"ReAct 而非 OpenAI 原生 function-calling 协议，tool 角色要求前驱 assistant 带 tool_calls/tool_call_id。已改回用 `role:"user"` 承载观察结果，稳定兼容。

19. **多租户隔离终极验证（项目核心卖点）✅✅**：租户 A(9988) 上传含"租户A的秘密是苹果香蕉梨"，租户 B(9) 上传含"租户B的秘密是橘子葡萄西瓜"，双方向量化。
   → 租户 B 问"租户A的秘密是什么？"→ 调用 knowledge_retrieve 检索自己知识库（tenant_id=9 强制过滤）→ 答"我不知道租户A的秘密是什么"。
   → 反向全验证：B问自己的秘密能答出"橘子葡萄西瓜"✅；A问自己的能答出"苹果香蕉梨"✅；A问B的"未找到，不知道"✅；B问A的未泄漏✅。**双向隔离全部生效，跨租户零泄漏**。
   → 根因在 storage `SearchVectors` 的 `tenant_id` 等值过滤（不传编译不过），业务层传错也被兜住——隔离兜底在数据层死守。


#### 周日・会话记忆 & 上下文压缩

- [ ] Redis 实现短期会话记忆
- [ ] 多轮对话历史存取
- [ ] 上下文长度检测
- [ ] 超长对话自动摘要压缩
- [ ] Agent 最大迭代轮次限制
- [ ] 测试：多轮对话，Agent 记住上下文
- [ ] 测试：超长对话触发摘要压缩
- [ ] 测试：循环场景达到最大轮次终止

### 第二周

#### 周一・异步任务 & MQ

- [ ] docker-compose 启用 RabbitMQ
- [ ] 封装 MQ 生产者
- [ ] 实现协程消费者
- [ ] 文档解析任务投递队列
- [ ] 任务状态管理：pending / running / success / failed
- [ ] 任务状态查询接口
- [ ] 测试：提交异步任务，状态正确流转
- [ ] 测试：任务失败，记录错误信息

#### 周二・权限管控 & 参数校验

- [ ] 租户工具白名单配置
- [ ] Agent 调用工具前校验权限
- [ ] 未授权工具直接拦截
- [ ] 完善接口参数校验
- [ ] 非法参数请求拦截
- [ ] 测试：未授权工具，Agent 无法调用
- [ ] 测试：非法参数返回 400，服务不崩溃

#### 周三・限流 & 用量统计

- [ ] Redis 实现租户维度 QPS 限流
- [ ] LLM 调用 Token 消耗统计
- [ ] 租户配额检查
- [ ] 超配额拦截
- [ ] 测试：高频请求触发限流
- [ ] 测试：每次 LLM 调用计数器递增

#### 周四・可观测体系

- [ ] 全链路 TraceID 生成与透传
- [ ] 结构化日志封装
- [ ] 日志携带 TraceID
- [ ] 接入 Prometheus 指标
- [ ] 核心指标埋点：请求量、LLM 调用次数
- [ ] 审计日志写入
- [ ] 测试：一条请求全链路 TraceID 一致
- [ ] 测试：Prometheus 能采集到指标

#### 周五・全链路联调 & 异常测试

- [ ] 端到端完整流程联调
- [ ] LLM 接口故障降级测试
- [ ] 工具执行失败容错测试
- [ ] 超长上下文处理测试
- [ ] 多租户并发隔离测试
- [ ] 并发会话测试
- [ ] 修复发现的 bug

#### 周六・文档整理 & 架构图

- [ ] 绘制系统架构图
- [ ] 完善 README
- [ ] 补充部署说明
- [ ] 整理工程取舍说明
- [ ] 后续演进方案
- [ ] 本地简易压测
- [ ] 收集量化指标
- [ ] 清理调试垃圾代码

#### 周日・面试准备 & 部署

- [ ] 梳理面试问答库
- [ ] 定稿简历项目描述
- [ ] 准备 3 个核心演示用例
- [ ] （可选）云服务器部署上线
- [ ] （可选）配置域名

## 🛠 技术栈

| 分类 | 技术 |
|------|------|
| 后端 | Go + Gin |
| 数据库 | MySQL 8.0 + GORM |
| 缓存 | Redis 7 |
| 对象存储 | MinIO |
| 向量库 | Qdrant |
| 消息队列 | RabbitMQ |
| 监控 | Prometheus |
| 大模型 | DeepSeek 对话（OpenAI 兼容）；向量走硅基流动（SiliconFlow）|
| 部署 | Docker Compose |

## 🚀 快速启动

```bash
# 1. 启动中间件
docker compose up -d

# 2. 复制配置文件
cp .env.example .env   # 修改 .env 中的配置

# 3. 数据库迁移
go run cmd/migrate/main.go

# 4. 启动服务
go run cmd/api/main.go
```

服务启动后访问：`http://127.0.0.1:端口/health`

## 📐 架构设计要点

- **多租户隔离**：单库多表逻辑隔离，全链路 `tenant_id` 透传
- **自研 Agent 引擎**：轻量 ReAct 调度，不依赖重型框架
- **插件化工具**：统一工具接口，按需注册，权限可控
- **分层记忆**：Redis 短期会话记忆 + 超长上下文自动摘要
- **全链路可观测**：TraceID 贯穿，指标埋点完善

## 📁 项目目录结构

> 说明：
> - `data/` 为 Docker 容器挂载的本地持久化数据（MySQL/MinIO/Qdrant/Redis），仅运行时生成，已在 `.gitignore` 排除**不入库**。
> - `.gitkeep` 为预留空包的占位文件，对应开发计划中尚未开工的模块。

```text
agent-platform/
├── README.md                  # 项目说明：计划 / 完成进度 / 结构 / 待办
├── .env.example               # 环境变量模板（cp 成 .env 后填入真实值）
├── .gitignore                 # 忽略 .env / data / bin / 日志 / IDE 配置等
├── go.mod / go.sum            # Go 模块依赖声明与锁定
├── docker-compose.yml         # 一键拉起 MySQL / Redis / MinIO / Qdrant / RabbitMQ
│
├── cmd/                       # 可执行程序入口（各独立 main）
│   ├── api/                   #   主服务入口：加载配置 → 连 MySQL/Redis/MinIO → 启动 Gin
│   ├── migrate/               #   数据库迁移工具（建表 / 加列 / 建索引）
│   ├── configtest/            #   配置自检工具（未配置输出调试日志，仅本地用）
│   ├── llm-demo/              #   llmclient 演示：go run ./cmd/llm-demo chat|embed
│   └── llm-selfcheck/         #   llmclient 自测：Chat/Embedding/token 全通过返回 ✅
│
├── config/
│   └── config.go              # 全局配置：读取 .env，注入 MySQL/Redis/MinIO/Qdrant/JWT/LLM/Server
│
├── api/                       # HTTP 接口层（Gin）
│   ├── router.go              #   路由注册：公开组（health/注册/登录）与私有组（JWT 鉴权）
│   ├── handler/               #   处理器：tenant.go / user.go / document.go / knowledge.go / chat.go
│   ├── service/               #   业务逻辑：与 handler 对应（调 storage，强制 tenant_id 过滤）
│   │   ├── document_parse.go  #   文档文本切分 SplitText + 读取 ReadTextDocument
│   │   │                      #   + 向量化主流程 ProcessDocument（切片→Embedding→写Qdrant）
│   │   ├── knowledge.go       #   知识库语义检索 Search（query转向量→storage.SearchVectors按租户过滤）
│   │   └── tool_permission.go #   工具权限校验 DBPermissionChecker（tenant_tool_config 白名单，默认开启知识库工具）
│   ├── middleware/            #   中间件：trace / recovery / logger / cors / JWT / context
│   ├── response/              #   统一返回格式与错误码工具
│   └── .gitkeep
│
├── storage/                   # 数据持久化层
│   ├── redis.go               #   Redis 客户端初始化 InitRedis（含 Ping 连通性校验 + 全局 RDB）
│   ├── mysql.go               #   MySQL 初始化（GORM）
│   ├── minio.go               #   MinIO SDK 封装 + 初始化：Upload/Download/GetURL/Delete
│   ├── qdrant.go              #   Qdrant 向量库封装：批量入库 UpsertVectors + 多租户检索 SearchVectors
│   ├── model/models.go        #   GORM 模型（7 张核心表的实体定义）
│   ├── tenant.go / user.go    #   租户 / 用户的数据库操作
│   ├── document.go            #   文档 CRUD（强制 tenant_id 过滤）
│   └── tool_config.go         #   租户工具权限配置 CRUD（GetToolConfig / SetToolConfig upsert）
│   └── document.go            #   文档元信息的数据库操作
│
├── splitter/                  # 文档切片策略（ChunkSize=600 / OverlapSize=80 常量 + 策略说明）
│   └── splitter.go
│
├── llmclient/                 # 大模型客户端（OpenAI 兼容接口）
│   ├── types.go               #   统一数据结构：Chat/Embedding(含Batch)/响应、角色、token 用量
│   └── client.go              #   Client 接口 + OpenAIClient 实现：超时/退避重试/多厂商/熔断/
│                               #   ChatWithJSON 结构化输出；Embed / EmbedBatch 向量生成
│
├── util/                      # 通用工具
│   ├── jwt.go                 #   JWT 生成与解析（载荷最小化：user_id/tenant_id/role）
│   └── password.go            #   密码 bcrypt 哈希与校验
│
├── docs/
│   └── db.md                  # 7 张核心数据表设计文档（字段 / 索引 / 关系）
│
├── agent/                       # 自研 Agent 引擎（ReAct 骨架，周五起）
│   ├── interfaces/interfaces.go #   AgentContext（多租户/会话上下文，下沉避免循环依赖）
│   ├── engine/                  #   ReAct 引擎：engine.go(主循环) / prompt.go(模板) / parser.go(解析) / llm_adapter.go(适配真实LLM) / types.go
│   ├── toolmanager/             #   工具注册中心 + PermissionChecker 接口
│   ├── toolkit/                 #   可插拔工具实现（echo 测试 / knowledge_retrieve）
│   └── memory/                  #   会话记忆（inmemory 当前）
├── mq/                        # （预留）消息队列封装（异步任务，第二周周一）
│   └── .gitkeep
├── service/                   # （预留）业务服务层（与 api/service 演进，后续整合）
│   └── .gitkeep
├── toolkit/                    # 可插拔工具集（Agent 调用能力注入）
│   ├── echo_tool.go            #   Echo 测试工具（骨架链路验证）
│   └── knowledge_retrieve.go   #   知识库检索工具（RAG 核心，调 service.Search，按 ctx.TenantID 隔离）
├── observability/             # （预留）可观测体系（TraceID / 指标 / 审计，第二周周四）
│   └── .gitkeep
│
└── data/                      # ⛔ 运行时数据，gitignore 排除不入库（本地 Docker 持久化）
    ├── mysql/                 #   MySQL 数据文件
    ├── redis/                 #   Redis dump 文件
    ├── minio/                 #   MinIO 对象文件
    └── qdrant/                #   Qdrant 向量数据
```

### 分层依赖关系

```text
cmd/(api...)  →  api/(handler → service)  →  storage/(MySQL/MinIO/Redis/Qdrant)
                          ↘  ↓                ↘  config/(全局配置)
                     llmclient/(Chat/Embedding/Batch)  ↑
                          ↘  ↓                ┌── util/(JWT/密码)
                     splitter/(文档切片)      └── qdrant.go(向量入库/多租户检索)
                             ↑  ↓
                     api/service/document_parse.go(ProcessDocument 编排)
```

> 核心原则：**业务层只依赖 `llmclient.Client` 接口与 `storage` 层**，不直接触碰厂商 SDK / DB 细节，便于换厂商、换存储。RAG 链路（切片 → Embedding → Qdrant 向量入库与多租户检索）已打通。

## ⚠️ 上线前需调整 / 调试期产物清单

以下为**当前开发调试阶段**留下的内容，部署到线上环境前必须处理，否则存在安全或稳定性风险。

### 1. 敏感配置（`.env`）
| 项 | 当前状态（调试期）| 上线要求 |
|----|------------------|---------|
| `JWT_SECRET` | 默认占位 `change-me-to-a-secret` | 必须替换为强随机密钥，否则 token 可被伪造 |
| `MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY` | 本地默认值 | 使用强凭证 |
| `MYSQL_ROOT_PWD` / `REDIS_PASSWORD` | 本地测试密码 | 使用强密码，生产禁用 root |
| `LLM_API_KEY` | 真实 key（调试填入）| 妥善保管，走密钥管理，禁止入库 |

> `.env` 已被 `.gitignore` 排除，不会再提交到仓库；但请勿把 `.env` 内容截图外泄。

### 2. 调试产物（代码 / 文件）
| 项 | 说明 | 上线处理 |
|----|------|---------|
| `cmd/configtest/` | 配置自检工具，会打印 `JWT_SECRET`、`LLM_API_KEY` 等配置 | 删除或仅本地调试 |
| `bin/api` | `nohup` 后台运行的编译产物 | 清理，改用 systemd / docker 正规进程托管 |

### 3. 需改为生产配置
| 项 | 当前（调试期）| 上线要求 |
|----|-------------|---------|
| `GIN_MODE` | 默认 debug 模式（启动有告警）| 设 `GIN_MODE=release` |
| CORS 跨域 | `cors.Default()` 允许 `*` 任意来源 | 限制为前端域名白名单 |
| 日志输出 | `fmt.Printf` 直接打到 stdout | 接结构化日志 + 文件 / 采集（logrus/zap）|
| 传输安全 | 直接暴露 `:8080` HTTP | 放 Nginx 反向代理 + HTTPS/SSL |
| DSN 时区 | `loc=Local`（本地时区）| 生产统一为 `loc=UTC` 避免时区歧义 |

> 注：租户接口、文档接口等均已接入 JWT 并**移入私有路由组**强制鉴权，不再存在公开待办。

## 🔧 后续需补齐项（当前为快速开发 / 调试期留的口子）

> 以下均为项目当前状态下为了快速开发与方便调试**暂时留下的简化点**，需要在**第二周或面试前**补齐。粗体为优先级最高的底线项。

### 🔴 安全相关（必须改，面试必问）

1. **创建租户接口权限**
   - 现状：创建租户已在**私有路由组**（需登录），但**任何登录用户（含普通 member）都能创建**，无超管角色限制
   - 问题：线上环境不可能让任意登录用户随便创建租户
   - 后续：增加 `super_admin` 超管角色，仅超管可建租户；或改成后台人工审核

2. **用户注册接口权限**
   - 现状：注册接口可传**任意 `tenant_id`**，谁都能注册
   - 问题：理论上应由租户管理员创建员工，而非任意注册
   - 后续：注册接口改为仅租户管理员可调用；或改为邀请制

3. **租户 ID 传递安全（最高优先级）**
   - 现状：**已落地并硬性验证** — JWT 中间件已将 `tenant_id` 注入 Context，提供 `GetTenantID` 工具；文档/检索/向量化全链路强制从 Context 拿 `tenant_id`；`storage.SearchVectors` 签名强制 `tenantID` 参数 + 内部 filter 强过滤（不传编译不过）
   - 已验证：**多租户隔离硬性对抗测试通过**（见周六问题小节第 10 条）——租户 B 搜租户 A 的机密数据"123456"完全搜不到，A 搜自己能搜到，双向无泄漏
   - 问题：仍需持续排查所有新增私有接口，一律从 Context 拿 `tenant_id`，前端传了也忽略（防止疏漏的接口绕过）

4. **密码强度校验**
   - 现状：注册时未校验密码强度
   - 问题：弱密码不安全
   - 后续：增加密码长度、复杂度校验

### 🟡 工程完善（建议改，体现工程化）

5. **统一错误码**
   - 现状：**已有基础错误码常量但未细分** — `response.go` 定义了 `CodeSuccess/CodeBadRequest/CodeUnauthorized/CodeForbidden/CodeServerError`（0/400/401/403/500）
   - 问题：错误码粒度较粗，业务细分不够
   - 后续：进一步定义细粒度错误码，如 `40001`=参数错误、`40101`=未登录、`40301`=无权限、`50001`=服务器错误，所有接口统一使用

6. **请求日志中间件**
   - 现状：**已有 `middleware.Logger`**，全局挂载，记录请求方法、路径、状态码、耗时，并带 `TraceID`
   - 问题：目前是用 `fmt.Printf` 打 stdout，非结构化、未落盘
   - 后续：升级为结构化日志（logrus/zap）+ 文件或采集，配合全链路 TraceID

7. **CORS 中间件**
   - 现状：**已有 `cors.Default()`** 全局中间件，能跨域，但允许任意来源
   - 问题：开发期够用，线上会暴露任意来源
   - 后续：改造为**受控 CORS**，允许前端域名白名单跨域，其余来源拒绝

8. **参数校验不统一**
   - 现状：**部分已有** — 用户注册/登录已用结构体 `binding:"required"` 标签 + `ShouldBindJSON`，但其余 handler（如租户接口）仍各自手写校验
   - 后续：统一用 `validator` 库做结构体标签校验，覆盖所有 handler，统一参数错误处理

9. **migrate 工具配置重复**
   - 现状：migrate 工具自己读 `.env`，api 服务也读 `.env`
   - 后续：统一复用 `config.Load()`，不要到处复制配置加载代码

### 🟢 功能补充（时间充裕再加）

10. **操作审计日志**
    - 现状：关键操作（创建租户、删除文档、登录等）未记审计日志
    - 后续：写入 `audit_log` 表，体现 SaaS 平台必备审计能力

11. **租户状态未拦截**
    - 现状：租户被禁用后，用户可能仍能登录
    - 后续：登录时检查租户状态，禁用租户禁止登录；JWT 中间件也可校验

12. **无刷新 token 机制**
    - 现状：`access_token` 过期就得重新登录
    - 后续：增加 `refresh_token`，用其换取新 `access_token`，不必每次都输密码

### 🎯 优先级排序（第二周 / 面试前按此顺序补）

| 优先级 | 事项 |
|--------|------|
| 🔴 最高 | **`tenant_id` 全部从 JWT 拿，不信前端** → 多租户安全底线 |
| 🔴 高 | 统一错误码、参数校验 → 工程化体现 |
| 🟡 中 | 请求日志、CORS → 完善度 |
| 🟢 低 | 刷新 token、审计日志 → 锦上添花 |

