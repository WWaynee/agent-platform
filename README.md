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
- **文档级智能检索**：可按 `document_ids` 限定在某几个文档里检索；检索结果携带来源文档名称，支持"对比文档 A 与 B / 总结整篇《xxx》"等整文档级问答（名称→ID 解析：`list_documents` / `search_documents` 匹配名称得 ID → `knowledge_retrieve` 限定检索 → 必要时 `get_document_content` 读全文）
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
- [x] 实现文档删除接口（DELETE /api/document/:id，MinIO + DB 同步删，**仅上传者可删的用户级隔离**）
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
7. **文档删除的用户级隔离**：多租户隔离只挡「跨租户」，不挡「同租户不同用户」——成员原可删管理员上传的同租户文档。
   → 与既有的会话隔离保持一致：`service.DeleteDocument` 增加 `userID` 参数，校验 `doc.UserID == userID`，否则返回"无权删除他人文档"。成员仅能删自己上传的文档，越权（含删 admin 文档）被拦截。

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

- [x] Redis 实现短期会话记忆
- [x] 多轮对话历史存取
- [x] 上下文长度检测
- [x] 超长对话自动摘要压缩
- [x] Agent 最大迭代轮次限制
- [x] 测试：多轮对话，Agent 记住上下文
- [x] 测试：超长对话触发摘要压缩
- [x] 测试：循环场景达到最大轮次终止

> 📌 周日追加成果（会话管理阶段）：**能创建会话 / 查会话列表 / 删会话** 已落地——
> - `storage/session.go`：CreateSession / GetSessionByID / ListSessions(分页+租户+用户过滤,按更新时间倒序) / DeleteSession(软删除)，所有查询强制 `tenant_id` 过滤。
> - `api/service/session.go`：CreateSession(返回会话ID) / GetSessionList(只当前用户,更新时间倒序) / DeleteSession(校验存在+只删自己的 → 软删DB → 同步删 Redis 消息历史，保持两端一致)。
> - 会话**元数据**落 MySQL `sessions` 表；会话**对话消息**存 Redis（`session:{tenant_id}:{id}:messages`）。会话主键 ID 即 Redis 里的 session_id。
> - 自测通过：创建会话 ✅、列表只当前用户 ✅、删除会话时 Redis 消息历史同步清理 ✅、越权删他人会话被拒 ✅。

> 📌 周日追加成果②（超长上下文自动压缩，已落地）：
> - 压缩下沉到 **Memory 层**（`agent/memory/compressing.go` 的 `CompressingMemory` 装饰器）：包裹底层 `RedisMemory` / `InMemoryMemory`，**在 `AddMessage` 时检查该会话历史总 token**，超阈值（`CompressThresholdTokens=2000`）即自动压缩——**业务层（引擎）无感知**，只正常拿历史 / 加消息。
> - 压缩内部逻辑：拆新旧 → 旧历史经注入的 `Summarizer`（`ReActEngine.Summarize`，真实 LLM）生成中文摘要 → 组装 `[system 摘要 + 最近 3 轮原文]` → 写回底层。**摘要失败（LLM）降级**为丢弃旧消息、保留最近几轮，不中断对话。
> - ⚠️ **修复过的一个 bug（超长上下文处理测试中发现）**：原实现为"防频繁套娃"，只要历史首条已是 system 摘要（说明刚压缩过）就**不再压缩**。这会让首次压缩后历史一直以 system 开头、之后**永不再压缩、token 无限膨胀**——实测 20 轮长对话 token 涨到 45556，远超 LLM 上下文窗口。
>   → 修复：`shouldCompress` 改为**超长即压缩、不再因首条是 system 而阻断**；压缩时首条旧 system 摘要会随其后旧消息一起被折叠进**更新的摘要**，保留最近几轮。修复后 20 轮长对话 token 稳定在 ~7220（不膨胀），历史首条始终是 system 摘要；新增单测 `TestCompressingMemory_RecompressAfterSystem`（含"首条已是 system 仍能二次压缩、token 回落"）回归。
>   → 端到端脚本 `scripts/e2e_long_context.sh`：连续 20 轮长对话验证自动压缩（token 稳定不回涨）、堆长后继续对话正常、5000 字/20400 字节超长问题被 `query max=2000` 参数校验直接 400 拒绝（未进 LLM）。
> - 生产装配（`cmd/api/main.go`）：`baseMem=NewRedisMemory` + `agentEngine`；再以 `agentEngine.Memory = NewCompressingMemory(baseMem, agentEngine)` 替换；`agentEngine` 即实现 `memory.Summarizer`。
> - 触发时机：每 `AddMessage` 后检查；阈值 / 保留轮数 / 结构见 `compressing.go` 常量。
> - 自测：短对话不压缩 ✅；超长对话自动压缩（单测用注入 Summarizer 的 mock，另用真实 Redis+LLM 冒烟验证）✅；压缩后 token 下降 ✅；摘要失败降级 ✅。`go test ./agent/...` 全绿。

> ✅ **已知设计取舍 / 待优化 → 已按需求单 0002「对话存储双轨分离」解决（2026-08-16）**：
> - 旧问题（见下）：压缩走 `base.Clear()` + `RPUSH` 重建，**被压缩掉的旧对话逐字原文从 Redis 被永久覆盖、不可回溯**，前端拿不到完整对话原始历史。
> - **方案（已落地）**：**双轨分离存储**——
>   - **冷轨（MySQL `chat_messages`）**：**完整原文、永不压缩、永不过期**。每轮落库 `question / tool_call(工具名+参数JSON) / tool_result(检索片段全文) / answer` 各类消息（`role`+`kind` 区分），**含工具调用全过程**，供前端回看 / 审计。`GET /api/session/:id/messages` 改读这份完整历史（带租户+本人校验）。
>   - **热轨（Redis `CompressingMemory`）**：仍只存"最终问答 + 工具**模板化摘要**"（`summarizeToolCall` 零额外 LLM 调用，以 user 角色写 `[工具] 工具名：…`），供下一轮 LLM 理解 + 参与超长自动压缩。模型视角不背工具原文、压缩不误伤。
>   - **解耦注入**：`agent/engine/sink.go` 新增 `FullHistorySink` 最小接口 + `SetFullHistorySink(...)`，`cmd/api/main.go` 用 `chatMsgSink` adapter 把 `storage.AppendChatMessage` 接进来；不注入则冷轨静默关闭。写入**异步后台 goroutine**（`persistFullHistory`，基于 `AgentContext.ToContext` 派生独立 ctx 保留 trace_id/tenant_id/user_id），单条失败只 warn（对齐审计的"尽力而为"），不阻塞对话。
>   - **类型统一 + 集中转换**：`ChatMsg.SessionID` 为 string（跟随 `AgentContext.SessionID`），adapter 内 `toStorageChatMessage`（经 `strconv.ParseUint`）统一转 uint64 落库；`ChatMsg` 不带 TraceID，trace_id 一律从传入的上下文取。
> - 新增端到端脚本 `scripts/e2e_history_dual_track.sh`（完整历史含工具调用 + 热轨摘要 + 越权 403 回归）。
> - 单测：`TestGetSessionMessagesReadsMySQL`（落库/读回/正序/kind/多租户隔离）、`TestSummarizeToolCall`（模板化摘要与截断、零 LLM）。`go vet ./...` / `go test ./...` / `go build ./...` 全绿。



### 第二周

#### 周一・异步任务 & MQ

- [x] docker-compose 启用 RabbitMQ
- [x] 封装 MQ 生产者
- [x] 实现协程消费者
- [x] 文档解析任务投递队列
- [x] 任务状态管理：pending / running / success / failed
- [x] 任务状态查询接口
- [x] 测试：提交异步任务，状态正确流转
- [x] 测试：任务失败，记录错误信息

#### 📌 周一遇到的问题与解决方案

1. **失败可重试 vs 防死循环**：失败一律 Nack 会无限重试占满队列，一律 Ack 又会丢可恢复任务。
   → 引入哨兵错误 `mq.ErrRequeue`：显式声明可重试才 `Nack(requeue=true)`，其余失败（含坏消息/解析失败）一律 Ack 防死循环；service 累加 `retry_count`，未到上限（3次）重入队，到上限置 `failed`。

2. **单条消息 panic 会拖垮 Worker**：handler panic 会崩掉消费协程甚至整个 Worker。
   → `mq.Consume` 用 `safeProcess`（defer/recover）把 panic 转成 error 按失败处理；实测投递非法消息 worker 不崩、后续消息照常处理。

3. **重活不能阻塞 API → 拆独立 Worker**：解析（读文件+切片+Embedding+写Qdrant）是重活。
   → 单独 `cmd/worker` 进程，只初始化解析所需中间件并阻塞消费；API 只管接请求、落任务、发 MQ。两者独立启停，**多开 worker 即横向扩容**。

4. **消息持久化 + 手动 ACK（不丢消息）**：默认非持久化且自动 ACK 都会丢消息。
   → `QueueDeclare(durable=true)` + `DeliveryMode=Persistent` + `autoAck=false`。实测 worker 停机时上传→消息积压→重启后消费成功，零丢失。

5. **重复消费要幂等**：同条消息可能被消费多次（重试/ACK前崩溃），重复向量化浪费且写重。
   → 消费入口先查文档状态，**已 success 直接跳过**；配合点 ID `(documentID<<32)|chunkIndex` 的 upsert 覆盖写，双重保证幂等。

6. **任务/文档状态需成对流转**：成败都要同步任务与文档状态并记 error_msg。
   → `ConsumeDocumentParseTask` 统一编排 processing→结果；新增 `UpdateTaskRetry` 同更 `retry_count/status/error_msg`。自测：成功流转、空文件双 failed+错误信息、租户隔离、并发 5 份全 success 零丢失，全部通过 ✅。

#### 周二・权限管控 & 参数校验

- [x] 租户工具白名单配置
- [x] Agent 调用工具前校验权限
- [x] 未授权工具直接拦截
- [x] 完善接口参数校验
- [x] 非法参数请求拦截
- [x] 测试：未授权工具，Agent 无法调用
- [x] 测试：非法参数返回 400，服务不崩溃

#### 📌 周二遇到的问题与解决方案

1. **"查不到配置"的默认策略要双保险**：既要兼容老租户（未初始化配置）不误拦截，又要让新租户在管理端能看到可管理的开关。
   → 新租户创建时显式初始化默认开启记录；老租户/新增工具查不到记录时，权限层**查不到即默认放行**。判断口径统一收敛在权限层一处，storage 只做纯 CRUD，职责清晰不打架。

2. **bool 开关字段的 gorm 默认值陷阱**：`IsEnable` 一旦带 `default:true` 标签，会因 bool 零值(false) 被 gorm 当"未赋值"而覆盖成 true，导致"关闭"操作失效、权限校验失灵。
   → 去掉该默认值标签（含 DB 层列默认值）。教训：bool 开关字段的默认值标签要格外小心。

3. **权限校验位置要收敛，不能散落各 handler**：工具权限若在各自 handler 判断容易漏，也和"插件化工具"设计冲突。
   → 工具权限统一放 **ToolManager 执行前**（通过 PermissionChecker 接口注入，所有工具调用天然拦截）；接口权限（admin）统一放**路由组中间件**，管理接口挂到 admin 组即自动受保护。

4. **参数校验的"两套标签"问题**：项目既有结构体全用 Gin 的 `binding:` 标签，统一校验引擎也用它。
   → 统一沿用 `binding:` 一套标签，让结构体既能被 Gin 识别、也能被统一引擎识别，避免"两套标签 + 双引擎"混乱。

5. **required 对数值得零值会误判**：租户状态 `status=0(禁用)` 是合法值，但 `required` 会把它当"未传"拦截。
   → 改用 `oneof=0 1`：既不误伤合法零值，又能拦截非法取值。

6. **统一校验错误要返回"具体哪个字段错 + 为什么错"**：仅返回 `400 参数错误` 前端无法定位。
   → 校验失败返回结构化字段错误 `data:{username:"…", password:"…"}`：字段名用 json/form 标签键，错误为中文提示（区分必填/字符串长度/数值 min/max/取值）。所有 handler 一行统一出口收敛，不再手写。

7. **只校验绑定、不校验"业务语义"**：校验标签能拦空值/长度，但拦不住"配置一个不存在的工具名"这类业务错误。
   → 写库前先校验工具是否已注册，未注册直接 400，避免把垃圾配置写进白名单表。

8. **多租户隔离必须在数据层死守**：租户 A 关闭工具不能影响租户 B。
   → 工具配置所有 CRUD 强制租户过滤，按租户独立开关，联合唯一约束保证同租户工具不重复。实测租户间互不影响。

#### 周三・限流 & 用量统计

- [x] Redis 分布式滑动窗口限流（ZSet + Lua 原子操作）
- [x] 租户级 + 用户级两层限流中间件（覆盖所有私有接口）
- [x] 对话接口专属更严格限流（调 LLM 成本高，单独计数）
- [x] LLM 调用 Token 消耗实时统计（复用 UsageReporter 钩子，按天 + 租户/用户维度）
- [x] 租户配额检查（QuotaLlmToken 字段，0 = 不限制）
- [x] 超配额拦截（对话接口返回 403 提示配额已用完）
- [x] 用量查询接口（当天用量 + 最近 N 天历史）
- [x] 新租户默认 token 配额（默认 100 万/月）
- [x] 测试：高频请求触发限流返回 429
- [x] 测试：每次 LLM 调用 Redis 计数器递增

#### 📌 周三遇到的问题与解决方案

1. **用量统计怎么把 tenant_id 传到 LLM 客户端**：一开始纠结是改 LLM 调用签名（显式传参）还是走 context 透传。显式传参要改业务调用链，侵入大。
   → 决定**复用已有 UsageReporter 钩子 + context 透传**（方案 B/README 预留位）：新增 `agent/interfaces/context.go` 定义 `WithTenantUser(ctx, tenantID, userID)` 和 `TenantIDFromCtx/UserIDFromCtx` 安全取读函数；`engine.Run` 每次调 LLM 前把租户/用户塞进 ctx，`api/service` 的 `UsageReporter.Report` 从 ctx 提取后 Redis 累加 —— **业务调用链（agent/service）零侵入**。

2. **限流必须分布式 + 原子，否则多实例不准**：本地内存计数在多副本部署时各自独立，阈值整体失效且不公平；并发请求还有竞态。
   → **用 Redis ZSet 实现滑动窗口**，**Lua 脚本保证原子性**（删旧 + 计数 + 写入 + 设过期一条龙）。成员值用"时间戳+随机数"唯一化，避免同一毫秒并发成员重复导致计数少算。Redis 故障时**保守放行**（限流组件挂了不把服务弄挂，但打印告警），一切可控。

3. **限流维度拆分（三层）**：单层限流防不了「单个租户整体打爆」和「单个用户恶意刷」两类问题。
   → 做成**租户级（默认 300/分）+ 用户级（默认 60/分）两层**叠加；**对话接口单独再限**（默认 20/分，因为调 LLM 成本高）。所有阈值都放 `config`，不写死。

4. **用量按天 + 多维度 key 设计**：每天调用次数多，写 MySQL 慢；还要能按天出趋势。
   → **只做 Redis 实时按天计数，不做 MySQL 持久化**（按你定的简化方案）：key 形如 `usage:tenant:{id}:{YYYY-MM-DD}:token/calls`、`usage:user:{id}:{date}:token/calls`，`INCR/INCRBY` 原子累加，设置 **30 天过期**自动清理，无需定时任务。查询接口直接从 Redis 读，支持最近 N 天历史。

5. **配额拦截要「只对打 LLM 的接口」生效**：上传文档、查列表等普通接口不合配额语义，对话才耗 token。
   → **配额中间件只挂在 `POST /api/chat`**（且放在限流中间件之后执行：限流→配额→业务）。读租户表 `QuotaLlmToken`（0=不限制），从 Redis 按月求和当月已用 token，超了返回 403"本月 token 配额已用完"。

#### 周四・可观测体系

- [x] 全链路 TraceID 生成与透传（HTTP 入口生成/透传 → Agent → LLM → 工具 → 存储全链路贯穿，MQ 生产/消费同链路）
- [x] 结构化日志封装（observability：JSON 结构化 + 统一字段 trace_id/tenant_id/user_id/latency）
- [x] 日志携带 TraceID（WithContext / WithAgentContext / 自定义 GORM logger 自动注入）
- [x] 接入 Prometheus 指标（observability/metrics.go：默认注册表 + MetricsHandler 暴露 /metrics）
- [x] 核心指标埋点：请求量(HTTP 中间件)、LLM 调用次数/token、工具调用、MQ 消息处理
- [x] Prometheus 指标独立内网端口暴露（服务端 `METRICS_PORT`，默认 9090，0=禁用；/metrics 不走公网业务中间件）
- [x] /health 详细健康检查（检查 MySQL/Redis/MinIO/Qdrant/RabbitMQ 五个依赖，返回各组件状态，异常返回 503，适配探针语义）
- [x] 审计日志写入（audit_log 表：RecordAuditLog 工具函数 + 上传文档/删除文档/修改工具配置/登录/创建会话/RAG问答 6 处埋点）
- [x] 测试：一条请求全链路 TraceID 一致（详见本日"遇到的问题与解决方案"）
- [x] 测试：Prometheus 能采集到指标（/metrics 返回标准文本格式 + 打点后计数变化）
- [x] 测试：健康检查各组件 up/down、异常时 HTTP 503、JSON 结构（status + components map + errors）

#### 📌 周四遇到的问题与解决方案

1. **trace_id 如何从 HTTP 入口贯穿到 Agent / LLM / 存储**：对话链路（handler → engine → 工具 → 存储）跨多个子包，易用裸 `context.Background()` 重建上下文把 trace_id 弄丢。
   → 建立**双向协议**（`agent/interfaces`）：标准 ctx 侧 `WithTraceID(ctx,id)`/`TraceIDFromCtx(ctx)`；`AgentContext` 侧 `WithTraceID()/TraceID()/ToContext(base)`。入口 `handler.Chat` 把请求级 trace_id 注入 `AgentContext`；engine 内取 logger 用 `observability.WithAgentContext(ctx)`、调 LLM/工具用 `ctx.ToContext(nil)`，**不再裸新建 ctx**。

2. **DB 慢查询/错误日志要带 trace_id**：GORM 默认 logger 打 stdout 无链路上下文。
   → 自定义 GORM logger（`storage/db_logger.go`）接入 `observability`，从 `DB.WithContext(ctx)` 透传的 ctx 读 trace_id/tenant_id 落统一 JSON；所有 storage CRUD 均 `DB.WithContext(ctx)`。

3. **MQ 生产/消费日志串同一条 trace_id**：生产者（HTTP 请求内）有 trace_id，消费者（独立 worker）没有 HTTP 上下文。
   → 生产方把 `TraceIDFromCtx(ctx)` 写入消息体（`DocumentParseMsg.TraceID`）；消费方取出 `msg.TraceID` 用 `WithTraceID` 重建消费 ctx，使"上传请求"与"异步消费"日志对齐，另加 `msg_id` 便于按消息追踪。

4. **日志字段要统一、可被采集系统解析**：各层手写 `fmt.Printf` 字段不规范难聚合。
   → 统一抽象到 `observability`：JSON 结构化 + 字段常量（`FieldTraceID/FieldTenantID/FieldUserID/FieldLatency/FieldError`）；`WithContext/WithTenantUser/WithAgentContext` 自动注入身份字段，`logger.Info/Error` 即带 trace_id。

5. **Prometheus 指标不能暴露公网，但又要能被采集**：`/metrics` 若挂在业务端口会公网可访问（泄露内部指标、耗资源），还经过登录/限流等业务中间件。
   → 独立启动一个**内网监听端口**（`config.Server.MetricsPort`，默认 9090，0=禁用）：`cmd/api/main.go` 单独 `go` 协程起 `http.Server`，只挂 `/metrics`（`observability.MetricsHandler()`），**不经 Gin 业务中间件**，只在内网可达，与公网业务端口隔离。
   ⚠️ 注意点：metrics 端口应只在内网/VPC 内可访问，或加鉴权，绝不能暴露到公网。

6. **指标标签维度太多会"基数爆炸"**：若把 `user_id`/`trace_id`/具体参数当标签，每个用户/请求都会新增一组时序，Prometheus 内存会扛不住。
   → 所有埋点标签只用**低基数枚举**：HTTP 用 `method/path/status_code`，LLM 用 `model/success`，工具用 `tool_name/success`，MQ 用 `queue/status`。trace_id/user_id 走日志字段、**绝不进指标标签**（见 `observability/metrics.go` 各 Vec 注释）。

7. **健康检查从"简单返回 running"升级为"反映依赖真实状态"**：最初的 `/health` 只固定返回 200+`{status:running}`，进程活着但依赖全挂也会被判"健康"，探针失去意义。
   → 重写成 `api/service/health.go` + `api/handler/health.go`：
   - 逐一检查 **MySQL(SELECT 1) / Redis(PING) / MinIO(查 bucket) / Qdrant(查集合) / RabbitMQ(开临时 Channel)**，每个返回 `up/down` + down 时带错误信息；
   - 返回 `{status, components:{mysql:up,...}, errors:{mysql:"..."}}`；
   - **任一依赖 down → overall unhealthy → HTTP 503**（K8s/Docker 探针据此剔除故障实例）；全部正常 → 200；
   - 每个检查带 3s 超时 + `WithContext` 打日志带 trace_id，避免依赖 HANG 卡死探针或丢失链路。
   ⚠️ 注意点：探针状态码语义——200=健康、503=故障，不要用 200 掩盖依赖故障；健康检查要设超时，否则依赖无响应会让探针自身挂起。

8. **健康检查返回值结构要方便探针/前端解析**：若 components 值用嵌套对象，探针脚本和前端都要多一层解析。
   → `components` 值统一为字符串 `"up"/"down"`（可直接判断），down 组件的具体错误信息放在独立的 `errors` map（仅 down 时存在、`omitempty` 省略），一个 JSON 里兼顾"简单判断"与"详细排查"。

9. **审计日志（audit_log 表）**：SaaS 平台关键操作要有据可查，体现审计能力。
   → 周一设计的 7 张表里已有 `audit_logs`（`tenant_id/user_id/operation/trace_id/content`），确认迁移已在 `AllModels` 里并执行过（MySQL 实测 `SHOW TABLES` 存在）。
   → 新增 `api/service/audit.go` 的 **`RecordAuditLog(ctx, operation, content)`** 工具函数：内部用 `interfaces.TenantIDFromCtx/UserIDFromCtx/TraceIDFromCtx` 统一从 ctx 提租户/用户/链路 ID，写入 `audit_logs`（写库走新增 `storage/audit.go` 的 `CreateAuditLog`）。**审计是"尽力而为"**：写库失败只打 warn、绝不阻断主业务。
   → 在关键操作成功后调用：上传文档、删除文档、修改工具配置、登录、创建会话、RAG问答。
   → ⚠️ **登录的特殊处理**：登录是公开接口，调用时 ctx 里还没有 `user_id`（未鉴权），故先 `WithTenantUser(ctx, user.TenantID, user.ID)` 把操作者补进 ctx 再记录，否则审计日志拿不到"谁登录了"。
   → 自测：单元测试（`api/service/audit_test.go`，连真实 MySQL 集成验证 tenant/user/trace 落库）；端到端冒烟（启动服务→登录→查表，确认 `operation='登录'`、`user_id=36`、`trace_id` 正确）。

#### 周五・全链路联调 & 异常测试

- [x] 端到端完整流程联调
- [x] LLM 接口故障降级测试（e2e_llm_fault.sh 8/8：超时/5xx/非JSON/熔断打开/半开恢复）
- [x] 工具执行失败容错测试（engine/tool_fault_test.go 5/5：不存在/坏参/超时/报错/未启用）
- [x] 依赖服务挂了服务不崩（e2e_dependency_down.sh 25/25：真实 stop MySQL/MinIO/Qdrant → 服务不崩、/health 正确 down、依赖接口返回明确错误、恢复容器自动回正）
- [x] 超长上下文处理测试（含发现并修复"防套娃导致永不二次压缩、历史无限膨胀"bug，见周日成果②补记）
- [x] 多租户并发隔离测试（已完成，详见下表）
- [x] 并发会话测试（e2e_session_isolation.sh 24/24：同用户多会话上下文各自独立）
- [x] 修复发现的 bug（工具执行超时兜底 / 超长上下文永不二次压缩 / mock常识与工具禁用识别 / 用量calls口径断言）

> 📌 周五追加成果③（多租户并发隔离测试 —— 项目核心卖点终极验证，已落地 ✅）：
> 在已有"周六多租户隔离验证"基础上，补齐**并发场景**与**会话越权**两类硬性验证，并新增测试脚本与一个接口：
>
> - **新增接口 `GET /api/session/:id/messages`（会话历史查询）**：
>   - `api/service/session.go` `GetSessionMessages(ctx, tenantID, userID, id)`：**先归属校验后取数**（多租户/多用户越权防护），从 Redis 读该会话消息历史返回 `[{role,content}]` 正序。
>   - 越权防线两道：① `storage.GetSessionByID(ctx, tenantID, id)` 带租户过滤查询，跨租户/不存在统一 `gorm.ErrRecordNotFound`（不区分"不存在"与"无权"，防横向探测）；② `s.UserID != userID` 再校验本人，跨用户一律拒绝。
>   - `api/handler/session.go` `GetSessionMessages`：越权统一返回 **403 "会话不存在或无权访问"**，不泄露任何内容。
> - **端到端并发隔离脚本 `scripts/e2e_tenant_isolation.sh`**（全新租户 A/B，各上传含独家机密"苹果香蕉梨 123456"/"橘子葡萄西瓜 789012"的文档）：
>   - *确定性向量隔离*：A 搜 `橘子葡萄西瓜 789012` 搜不到 B / B 搜 `苹果香蕉梨 123456` 搜不到 A（双向）；
>   - *越权文档*：B 拿 A 的文档 ID 查详情/删除 → 均 400 "文档不存在"（A 文档完好未删）；
>   - *越权会话*：B 拿 A 的会话 ID 查历史 → **403**；删除 → 400（新增接口的越权防护生效）；A/B 查自己的历史均正常；
>   - *并发提问*：A 并行问"橘子葡萄西瓜是什么"、B 并行问"苹果香蕉梨是什么"，**两端回答均不含对方机密** → 并发下隔离依然有效。
>   - 实测：**18 项断言全过 / 失败 0 项 / 无任何数据越权**（含并发两 chat 均 0s 收敛、各自只基于自身检索结果作答）。
> - **mock 增强（`tools/fault_inject_llm`）**：ok 模式升级为**知识问答式(ReAct 驱动)**应答 —— 首次提问返回 `knowledge_retrieve` 动作触发检索；引擎喂回"知识库检索观察"后基于观察 `final_answer`（只引述观察内容、绝不凭自身编造）→ 使端到端（尤其并发隔离）验证真实走通"提问→检索→作答"，且从 LLM 侧同样保证"搜不到对端片段、不越权"。
> - 全部 `go vet` / `go test ./...` / `go build ./...` 通过。

> 📌 周五追加成果④（同一用户多会话并发隔离测试，已落地 ✅）：
> 验证"同一用户开多个会话，每个会话上下文各自独立、互不串"——这是 SaaS 多租户下**同租户内多会话**的基础隔离。
> - **新增端到端脚本 `scripts/e2e_session_isolation.sh`**（场景直接对应 PRD）：
>   - 全新租户 → 注册普通用户 → 建 3 个会话（会话1/2/3）；
>   - 每个会话各说一句自我介绍：会话1"我叫张三"、会话2"我叫李四"、会话3"我叫王五"；
>   - 三会话并发问"我叫什么？"，验证各自回答对应**自己的**上下文，互不串；
>   - 自测点①不同会话上下文不串：会话1→选？否，应返回张三、会话2→李四、会话3→王五，且各自答案绝不含另两个名字；
>   - 自测点②并发情况下依然隔离：三会话**同时**提问仍各自答对；
>   - 自测点③会话列表能看到所有会话（`GET /api/session/list` 含全部 3 个会话）。
>   - 实测：**24 项断言全过 / 失败 0 项**，会话之间完全隔离、上下文不串、并发下依然有效。
> - **mock 增强（会话内自我介绍记忆）**：`tools/fault_inject_llm` 的 `decideReActAction` 增加会话记忆推断——引擎把本会话（Redis 按 tenant+session 隔离）历史整体拼进 messages 喂给 LLM，mock 从中提取"我叫X/我的名字是X/名字叫X"；当本消息是自我介绍即确认记住；当询问"我叫什么"时，基于**本会话**历史记忆作答（`根据本会话前的记忆，你叫X`）。
>   - 🧠 隔离要点：mock 只从**当前会话**的 messages 历史取名字，而历史本就按 `session:{tenant}:{sid}` 隔离存储 → 从 LLM 侧同样保证"会话之间上下文不互通"。
>   - 踩坑：`strings.Index` 返回**字节偏移**，最初误用 `len([]rune(kw))` 取 offset 导致中文截断乱码（"你叫叫张三"），修复为与字节偏移配套的 `len(kw)`。
> - **验证根因**：chat 层 `session_id` 关联 Redis key（存储层天然按会话隔离）+ 会话列表/详情带 `tenant_id+UserID` 过滤 —— 同一用户不同会话的上下文各自独立、互不复用。
> - 全部 `go vet` / `go test ./...` / `go build ./...` 通过。

> 📌 周五追加成果⑤（限流功能端到端验证，已落地 ✅）：
> 验证限流**真实生效**（不再是只看代码/单测），对应第二周周三的限流实现，覆盖全部 4 个自测点。
> - **新增端到端脚本 `scripts/e2e_rate_limit.sh`**（场景直接对应 PRD：快速刷请求→超阈值 429→窗口后自动恢复→租户 A 限流不影响租户 B→对话接口比普通接口更严格）：
>   - **① 超限返回 429（普通接口）**：用户快速刷 `GET /api/session/list` 70 次 —— 前 60 次 200、**第 61 次起返回 429**（命中用户级阈值 `UserPerMin=60`，默认 `RATE_LIMIT_USER_PER_MIN=60`）；
>   - **② 限流后自动恢复**：等待 62s（窗口 `RATE_LIMIT_WINDOW_SEC=60` 滑出）后**同一用户**再发 —— 恢复 200；
>   - **③ 多租户限流独立**：租户 A 用户打满（持续 429）时，**全新租户 C 3 次请求全部 200** → 限流 key 按租户/用户各自隔离（Redis `ratelimit:tenant:{id}` / `ratelimit:user:{id}`），A 被打爆不拖累其它租户；
>   - **④ 对话接口限流更严格**：快速刷 `POST /api/chat` —— **第 21 次即返回 429**（对话专属阈值 `ChatPerMin=20`，`RATE_LIMIT_CHAT_PER_MIN=20`），对比普通接口第 61 次才 429 → **对话因为是调 LLM 高成本接口，单独更严格、更早拦截**。
> - **实现复盘（链路已在第二周周三落地）**：`api/middleware/ratelimit.go` 三层滑动窗口（租户级 `TenantPerMin` + 用户级 `UserPerMin` + 对话级 `ChatPerMin`），底层 `storage.AllowRequest` 走 **Redis ZSet + Lua 原子脚本**（分布式、并发一致）；`POST /api/chat` 先过普通 `RateLimiter` 再叠加 `ChatRateLimiter` 与 `QuotaInterceptor`（限流→配额→业务）；阈值全在 `config`（`RATE_LIMIT_*` 环境变量）不写死。
> - 实测：4 个自测点全过 / 失败 0 项（多租户独立 + 对话更严格尤其关键，双重证明"限流独立+分层严格"）。
> - 全部 `go vet` / `go test ./...` / `go build ./...` 通过。

> 📌 周五追加成果⑥（核心接口性能巡检，已落地 ✅）：
> 目标"看核心接口性能、有无明显慢接口"，新增端到端脚本 `scripts/e2e_perf_check.sh`，对应 5 个自测点全部实测：
> - **① 普通接口响应时间 < 100ms**：文档列表 avg **3.1ms** / 会话列表 avg **3.2ms**（各 15 次采样，远低于 100ms 目标，单次最大也 ~21ms 内）✅
> - **② 对话接口响应时间**：简单问题走本地 ReAct + mock LLM avg **~350-470ms/次**，耗时**主要花在 LLM 环节**（本地 Redis/路由极快）；真实 DeepSeek 会更高（1-5s），属 LLM 网络+推理正常耗时 ✅
> - **③ 慢查询检查**：全程 **0 条 GORM `>=100ms` 慢查询日志**——`storage/db_logger.go` 内置 `dbSlowThreshold=100ms`，本次大量列表/对话请求期间无任何慢 SQL，**DB 层索引/查询无需优化** ✅
> - **④ 并发能力**：并发 10（含混合 40 请求压力）**全部 200 不崩**，并发 avg **4.3-19.9ms**、max ~59ms——对比单请求 ~3ms，延迟仅随并发个位数倍增长、**无指数级增长**，压测后 `/health` 健康 ✅
> - **⑤ 没有内存泄漏**：多轮(每轮 30-100 请求)负载后 api 进程 **RSS 收敛在 ~36-38MB 稳定水位**，第 2 轮后停止增长、结束甚至回落到 36.8MB——Go 堆复用达标，**无每轮持续攀升的泄漏特征** ✅
> - **结论**：核心接口（文档/会话列表、对话）响应健康、无慢 SQL、并发稳定、无内存泄漏；**无需加索引或额外优化**。若日后真实 LLM 接入导致对话慢，属外部网络/推理耗时，非本地短板。
> - 全部 `go vet` / `go test ./...` / `go build ./...` 通过。

> 📌 周五主流程回归确认（周五当天 12 个验证点全部跑通，已落地 ✅）：
> 逐个跑完周五清单里新用户旅程 / 管理权限 / 多角色 / 存储一致 / LLM 故障 / 工具容错 / 依赖故障 / 超长上下文 / 多租户隔离 / 会话隔离 / 限流 / 性能共 12 项，**全部通过、失败 0 项**；`go test ./...` 14 个包全绿；并跑通修完 bug 后的主流程回归（用户旅程 18/18、管理 16/16）。

> ⚠️ 周五测试注意点（已修复/已确认，供后续参考）：
> - **用量口径**：用量统计的 `calls` 计的是「LLM 调用次数」而非「对话提问次数」——ReAct 回答一个问题会多轮调 LLM（决策+检索+作答），故一次提问可能记 2 次及以上。查用量别按「对话数=调用数」对账。
> - **对话"常识问题/工具被禁"的表达**：测试用的本地 mock LLM 已补上对常识问题直接作答、对已禁用工具明确"未启用"的处理，避免出现"反复尝试调用同一禁用工具、最终只回未收敛"的僵硬答复；真实 LLM 会更好地自然回答。
> - **日志与工具**：本轮新增的工具超时兜底（坏工具不无限等待）、RabbitMQ 自动重连（挂掉恢复后无需重启）、LLM 超时/5xx/熔断降级，均已生效并有单测/联调覆盖。
> - **依赖故障验证的覆盖口径（已补齐停容器端到端）**：Redis 挂掉（引擎不 panic、可兜底回答）与 RabbitMQ 挂掉（不 panic + 自动重连）有**专门故障单测**（`agent/engine/fault_redis_test.go`、`mq/reconnect_test.go`）；MySQL/MinIO/Qdrant 除了「访问失败即返回明确错误/降级」+ `/health` 探活（`TestHealthHandler_DownReturns503` 单测），**还新增停容器端到端验证 `scripts/e2e_dependency_down.sh`**：真实 `docker compose stop` 掉 MySQL / MinIO / Qdrant，逐一验证「API 进程不崩、/health 正确报该依赖 down(503)、依赖接口返回明确错误(HTTP 200+code=500"connect refused"，不卡死)、无关接口仍 200、恢复容器后自动回正(无需重启 api/worker)」，实测 **25 项全过 / 失败 0 项**。

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
| 对象存储 | 阿里云 OSS（原 MinIO，需求单 0010 迁移）|
| 向量库 | Qdrant |
| 消息队列 | RabbitMQ |
| 监控 | Prometheus（observability/metrics.go，独立内网端口 /metrics，指标：HTTP/LLM/工具/MQ）|
| 可观测 | 结构化 JSON 日志（observability，JSON 采集器可直接对接）+ 全链路 TraceID + 详细 /health 健康检查 + 操作审计日志（audit_logs 表）|
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
go run cmd/api/main.go        # API 服务（接请求、上传时投递 MQ 消息）
go run cmd/worker/main.go     # Worker 独立进程（消费 document_parse 队列、执行异步解析，可多开扩容）
```

> **对象存储（需求单 0010）**：文档对象存储使用**阿里云 OSS**（原 MinIO 已迁移）。需在 `.env` 配置 `OSS_REGION / OSS_ENDPOINT / OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET / OSS_BUCKET`（见 `.env.example`）。文档下载/预览走 OSS **预签名 URL 直链**（`GET /api/document/:id/url`）。

服务启动后访问：
- 健康检查（详细依赖状态）：`http://127.0.0.1:{HTTP_PORT}/health`（全部正常 200，任一依赖挂 503）
- Prometheus 指标（独立内网端口）：`http://127.0.0.1:{METRICS_PORT}/metrics`（默认 9090，0=禁用）

## 🖥️ 前端界面（web/，需求单 0004）

系统内置一个**零构建纯静态前端**（`web/` 目录）：登录/注册页、三栏主工作台、管理后台。技术栈为原生 HTML + JS + Tailwind CDN，无需安装依赖、无需构建，直接丢给 Nginx/任意静态服务器即可。

**后端登录体验改造（需求单 0004 起）**：`POST /api/user/login` 的 `tenant_id` 改为**可选**——不传则按「用户名」全局登录（用户名全库唯一直接登录；多租户同名返回明确错误引导），前端登录页只需「用户名 + 密码」。传 `tenant_id` 的老调用方式完全兼容。登录/注册接口返回的 `user` 对象已对 `PasswordHash` 脱敏（不再泄露密码哈希）。

前端快速本地预览（开发期，浏览器直接打开 HTML 即可，注意后端 CORS 默认允许任意来源）：
```bash
# 方式一：Nginx 反代（推荐，见 deploy/nginx.conf）
# 把 web/ 挂到 Nginx root，/api 反代到 http://127.0.0.1:8080

# 方式二：仅开发预览 HTML（路由/user等接口需后端 CORS 允许）
open web/login.html   # 或 python3 -m http.server 后访问
```
页面：
- `login.html`：登录 + 租户注册（注册成功自动登录进入工作台）
- `index.html`：三栏工作台（左会话列表 / 中对话区 / 右文档管理）
- `admin.html`：管理后台（工具开关 + 用量统计，仅 admin 角色可见入口）

> ⚠️ 前端要点：业务错误为 HTTP 200 + `body.code!=0`（仅鉴权失败是真正 401）；删除文档仅上传者本人可删；对话无流式（普通 JSON 全量返回）；限流（用户 60/分、对话 20/分），文档任务轮询间隔 ≥2s 防撞 429。

## ⚠️ 注意事项（精要）

- **启动顺序**：先 `docker compose up -d` 拉起全部中间件，再跑 `migrate` 建表，最后启 `api`（如需要异步解析再加 `worker`）。
- **重启不丢**：会话消息 / 用量 / 限流计数在 Redis；文档与元数据在 MySQL/MinIO，内存里没有业务状态，重启安全。
- **可观测为结构化 JSON**：日志统一走 `observability` 输出 JSON（含 `trace_id/tenant_id/user_id/latency`），可按采集器直接接入。配置 `LOG_FILE` 后可经 **lumberjack 按日切分文件并只保留最近 7 天**（需求单 0011，见 `.env.example`）。
- **trace_id 贯穿**：HTTP 入口生成/透传，经 Agent/LLM/工具/存储，并随 MQ 消息带到消费端；排查问题先看 trace_id。
- **日志别打敏感信息（红线）**：密码、`api_key`、token、用户隐私数据一律不落日志（脱敏或干脆不打），这是安全底线。
- **指标标签要低基数**：只用 `method/status_code/model/tool_name/queue` 等枚举值，**绝不把 `user_id/trace_id/参数` 当标签**，否则 Prometheus 基数爆炸。
- **`/metrics` 不要暴露公网**：独立 `METRICS_PORT`（默认 9090）内网访问，或加鉴权，防止泄露内部指标。
- **`/health` 是探针语义**：任一依赖 down 返回 **503**（编排系统据此剔除故障实例），不要用 200 掩盖依赖故障。
- **多租户隔离底线在数据层死守**：新增任何跨租户查询，一律从 ctx/JWT 取 `tenant_id`，不要信前端传入。
- **bool 开关勿用 `gorm:"default:true"`**：零值 false 会被当"未赋值"覆盖成 true，导致"关闭"失效（详见第二周周二）。
- **生产部署前**：务必处理下方"上线前需调整"清单（换密钥、GIN_MODE=release、接采集、限 CORS）。

## 📐 架构设计要点

- **多租户隔离**：单库多表逻辑隔离，全链路 `tenant_id` 透传
- **自研 Agent 引擎**：轻量 ReAct 调度，不依赖重型框架
- **插件化工具**：统一工具接口，按需注册，权限可控
- **分层记忆**：Redis 短期会话记忆 + 超长上下文自动摘要
- **全链路可观测**：TraceID 贯穿（HTTP→Agent→LLM→工具→存储→MQ 生产/消费），统一结构化 JSON 日志

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
│   ├── worker/                #   独立消费者进程：初始化中间件 → 监听 document_parse 队列，异步执行文档解析
│   ├── migrate/               #   数据库迁移工具（建表 / 加列 / 建索引）
│   ├── configtest/            #   配置自检工具（未配置输出调试日志，仅本地用）
│   ├── llm-demo/              #   llmclient 演示：go run ./cmd/llm-demo chat|embed
│   └── llm-selfcheck/         #   llmclient 自测：Chat/Embedding/token 全通过返回 ✅
│
├── deploy/                    # 部署参考配置（需求单 0004）
│   └── nginx.conf             #   Nginx 反代参考：web/ 静态托管 + /api 反代到后端(Gin 8080) + 上传10MB/对话超时
│
├── web/                       # 前端静态资源（需求单 0004，零构建纯静态）
│   ├── login.html             #   登录 + 租户注册（登录取名全局登录，注册成功自动登录）
│   ├── index.html             #   三栏主工作台（左会话/中对话/右文档）
│   ├── admin.html             #   管理后台（工具开关 + 用量统计，仅 admin）
│   ├── css/style.css          #   少量自定义样式
│   └── js/                    #   api.js(请求封装/401/业务码) · auth.js(鉴权/登录态)
│                              #   chat.js(会话+对话) · document.js(文档管理) · admin.js(后台逻辑)
│
├── config/
│   └── config.go              # 全局配置：读取 .env，注入 MySQL/Redis/MinIO/Qdrant/JWT/LLM/Server
│
├── api/                       # HTTP 接口层（Gin）
│   ├── router.go              #   路由注册：公开组 / 私有组（JWT 鉴权）/ 管理组 admin（JWT+AdminAuth）
│   ├── handler/               #   处理器：tenant.go / user.go / document.go / knowledge.go / chat.go / session.go / task.go / tool_admin.go / usage.go / health.go(详细健康检查，依赖异常返503)
│   ├── service/               #   业务逻辑：与 handler 对应（调 storage，强制 tenant_id 过滤）
│   │   ├── document_parse.go  #   文档文本切分 SplitText + 读取 ReadTextDocument
│   │   │                      #   + 向量化主流程 ProcessDocument（切片→Embedding→写Qdrant）
│   │   ├── task.go            #   异步任务消费编排 ConsumeDocumentParseTask（任务+文档状态流转、重试计数、
│   │   │                      #   幂等短路） + 任务详情查询 GetTaskDetail + 任务列表 GetTaskList(分页, 管理员)
│   │   ├── knowledge.go       #   知识库语义检索 Search（query转向量→storage.SearchVectors按租户过滤）
│   │   ├── tool_permission.go #   工具权限校验 DBPermissionChecker（tenant_tool_config 白名单：显式关闭即拒，查不到默认放行）
│   │   ├── tool_admin.go      #   工具开关配置（租户管理）：GetToolEnabled / UpdateToolEnabled（查不到默认启用）
│   │   ├── session.go         #   会话业务：CreateSession(返回ID) / GetSessionList(只当前用户,更新时间倒序) / DeleteSession(软删DB+同步删Redis消息)
│   │   │                      #   + GetSessionMessages(会话历史查询：带租户过滤+UserID本人校验，越权统一拒绝；
│   │   │                      #     改读 MySQL chat_messages 冷轨完整原文，返回 {role,content,kind}，kind 区分 question/answer/tool_call/tool_result)
│   │   ├── usage.go           #   Token 用量统计业务：实现 llmclient.UsageReporter 钩子（从 ctx 取租户/用户→Redis 累加）+ 当天/历史查询
│   │   ├── quota.go           #   租户 Token 配额校验：CheckTenantTokenQuota（读 QuotaLlmToken + 当月 Redis 用量对比）
│   │   ├── health.go          #   依赖健康检查：CheckMySQL/Redis/MinIO/Qdrant/RabbitMQ（各带超时）+ CheckAll 聚合(任一 down→unhealthy)
│   │   └── audit.go           #   审计日志 RecordAuditLog(ctx,operation,content)：从 ctx 提 tenant/user/trace_id 写 audit_logs（尽力而为，失败只 warn）
│   ├── middleware/            #   中间件：trace(生成/透传trace_id) / recovery / logger(结构化带trace) / cors
│   │                          #   / JWT / admin / context / ratelimit(限流) / quota(配额)
│   ├── response/              #   统一返回格式与错误码工具
│   ├── validator/             #   统一参数校验（go-playground/validator v10）：BindJSON 绑定+校验
│   │                          #   + HandleBindError 统一出口（校验失败返回 400 + 结构化字段错误 data）
│   └── .gitkeep
│
├── storage/                   # 数据持久化层
│   ├── redis.go               #   Redis 客户端初始化 InitRedis（含 Ping 连通性校验 + 全局 RDB）
│   ├── mysql.go               #   MySQL 初始化（GORM）
│   ├── db_logger.go           #   自定义 GORM Logger：从 ctx 提 trace_id/tenant_id，慢查询/错误落统一 JSON 日志
│   ├── minio.go               #   对象存储封装（阿里云 OSS SDK，Upload/Download/PresignURL/Delete；原 MinIO 迁移）
│   ├── qdrant.go              #   Qdrant 向量库封装：批量入库 UpsertVectors + 多租户检索 SearchVectors
│   ├── model/models.go        #   GORM 模型（全核心表的实体定义）
│   ├── tenant.go / user.go    #   租户 / 用户的数据库操作
│   ├── document.go            #   文档 CRUD（强制 tenant_id 过滤）+ SearchDocuments（名称LIKE/租户过滤/排除软删）+ ReadDocumentContent（带租户过滤读 MinIO 全文）
│   ├── session.go             #   会话 CRUD：CreateSession / GetSessionByID / ListSessions(分页+租户+用户过滤,更新时间倒序) / DeleteSession(软删)
│   ├── task.go                #   异步任务 CRUD（强制 tenant_id 过滤）：CreateTask / UpdateTaskStatus / UpdateTaskRetry / GetTaskByID / ListTasks(分页)
│   ├── tool_config.go         #   租户工具权限配置 CRUD（GetToolConfig / ListToolConfigs / UpdateToolConfig / InitDefaultToolConfigs）
│   ├── audit.go               #   审计日志存储：CreateAuditLog（写 audit_logs 表，FromCtx 取上下文）
│   ├── chat_message.go        #   对话消息完整历史(冷轨)存储：AppendChatMessage / ListChatMessagesBySession(带租户过滤,正序)
│   └── ratelimit.go / usage.go#   Redis 限流与用量统计存储：AllowRequest(滑动窗口 Lua) + AddUsage/GetDayUsage/GetMonthUsage/GetRangeUsage
│
├── splitter/                  # 文档切片策略（ChunkSize=600 / OverlapSize=80 常量 + 策略说明）
│   └── splitter.go
│
├── llmclient/                 # 大模型客户端（OpenAI 兼容接口）
│   ├── usage.go               #   Token 用量封装：UsageReporter 回调钩子接口 + UsageEvent(含 ctx) + 内置累计统计
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
│   │   │                       #   含 trace_id 贯穿：WithTraceID()/TraceID()/ToContext(base)——把 AgentContext 承载的
│   │   │                       #   tenant/user/trace 合并译成标准 ctx，调 LLM/存储不再丢 trace_id
│   │   └── context.go           #   标准 ctx 透传：WithTraceID/TraceIDFromCtx + WithTenantUser/TenantIDFromCtx/UserIDFromCtx
│   ├── engine/                  #   ReAct 引擎（调度核心）
│   │   │                       #     engine.go: 主循环 Run(拿历史→拼Prompt→调LLM→解析→执行工具→持久化)
│   │   │                       #     sink.go: 冷轨完整历史写入接口 FullHistorySink + ChatMsg + SetFullHistorySink(可关闭)
│   │   │                       #     summarize_tool.go: 工具调用模板化摘要 summarizeToolCall(零额外LLM, [工具] 工具名：结论, 截断80字)
│   │   │                       #     （persist 双轨: ①热轨 Memory 最终问答+工具摘要 ②冷轨异步落 MySQL chat_messages）
│   │   │                       #     prompt.go: 动态拼装 SystemPrompt(角色+工具列表+JSON格式)
│   │   │                       #     parser.go: 多层容错解析 LLM 输出为 {action,action_input}
│   │   │                       #     llm_adapter.go: engine.Message ↔ llmclient.ChatMessage 适配
│   │   │                       #     compress.go/compressor.go: 提炼"超长历史→摘要"的压缩/摘要逻辑
│   │   │                       #     types.go: ChatRequest/Message/AgentResponse/ToolCall
│   │   │                       #     (engine.Summarize 实现 memory.Summarizer, 供压缩调用)
│   ├── toolmanager/             #   工具注册中心 tool.go/toolmanager.go + PermissionChecker 接口
│   ├── toolkit/                 #   可插拔工具实现（echo_tool 测试 / knowledge_retrieve RAG）
│   └── memory/                  #   会话记忆（接口设计 + 两层实现 + 超长自动压缩装饰器）
│       ├── memory.go            #   Memory 接口(GetHistory/AddMessage/Clear/Truncate)
│       ├── inmemory.go          #   内存版(单测/smoke)
│       ├── redis.go             #   Redis 版(落 Redis, 生产; RPUSH/LRANGE/LTRIM/Redis List)
│       ├── compressing.go       #   CompressingMemory 装饰器——AddMessage 超长自动检测
│       │                        #     token>2000 即压缩, 引擎无感知(摘要失败降级/防套娃)
│       ├── summarizer.go        #   Summarizer 接口(把历史折叠成摘要; 引擎注入实现)
│       │                        #     redis key: session:{tenant}:{sid}:messages
│       └── *_test.go            #   各实现对应单测
├── mq/                        # 消息队列封装（异步任务，第二周周一）
│   ├── message.go             #   消息 DocumentParseMsg(task_id/tenant_id/document_id + msg_id/trace_id) + 生产者 PublishDocumentParseTask(ctx, ...)
│   └── rabbitmq.go            #   RabbitMQ 基础客户端：InitRabbitMQ 连接+建Channel+声明队列(durable) /
│                              #   Publish 发消息(持久化 Persistent, 取 ctx 的 trace_id 写入消息体) / Consume 消费(手动ACK)：
│                              #   - 返回 nil → Ack；包装 ErrRequeue → Nack 重入队重试；其他失败/panic → 也 Ack（防死循环）
│                              #   - safeProcess 用 defer/recover 捕获 handler panic，单条消息异常不拖垮 worker
│                              #   - WithTraceID 重建消费 ctx，串起"上传请求 ↔ 异步消费"同一条 trace_id
├── service/                   # （预留）业务服务层（与 api/service 演进，后续整合）
│   └── .gitkeep
├── toolkit/                    # 可插拔工具集（Agent 调用能力注入）
│   ├── echo_tool.go            #   Echo 测试工具（骨架链路验证）
│   ├── knowledge_retrieve.go   #   知识库检索工具（RAG 核心，调 service.Search 按 ctx.TenantID 隔离；支持可选 document_ids 限定文档检索 + 返回 document_name/document_id/chunk_index）
│   ├── list_documents.go       #   文档列表工具（返回当前租户文档 id/name/size/status，上限50篇，带摘要）
│   ├── search_documents.go     #   文档名称搜索工具（LIKE 模糊搜索名称含关键字，上限20篇，名称→ID 解析的 B 路径）
│   └── get_document_content.go #   文档全文读取工具（读整篇全文，按 MaxDocumentChars=8000 字符截断 + 超长优先展示已生成摘要）
├── observability/             # 可观测体系（结构化 JSON 日志唯一出入口 + Prometheus 指标，第二周周四）
│   ├── logger.go              #   日志封装：JSON 结构化 + 字段常量(FieldTraceID/FieldTenantID/FieldUserID/FieldLatency/FieldError)
│   │                          #   + WithContext(ctx 自动注入 trace/tenant/user 字段) / WithAgentContext(Agent 链路) / WithTenantUser
│   ├── logger_test.go         #   单测（trace_id/tenant_id 字段注入校验）
│   ├── metrics.go             #   Prometheus 指标：HTTP请求计数/耗时、LLM调用/token、工具调用、MQ消息 + MetricsHandler 暴露 /metrics
│   │                          #   （低基数标签 method/status_code/model/tool_name/queue，杜绝 user_id/trace_id 防基数爆炸）
│   └── metrics_test.go        #   单测（各埋点打点 + /metrics HTTP 端点标准文本格式 + 打点后计数变化）
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

异步链路（第二周周一）：
cmd/api(上传)  →[mq.PublishDocumentParseTask]→ RabbitMQ(document_parse 队列)
                                                           ↓ 手动 ACK
                                          cmd/worker(Consume) → api/service.ConsumeDocumentParseTask
                                                      （解析+任务/文档状态流转+重试+幂等）
                                                                 ↓
                                          ProcessDocument(切片→Embedding→写Qdrant)
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
| `bin/api` / `bin/worker` | `nohup`/后台运行的编译产物（API 与 Worker 独立二进制） | 清理，改用 systemd / docker 正规进程托管（可多实例扩容 Worker） |

### 3. 需改为生产配置
| 项 | 当前（调试期）| 上线要求 |
|----|-------------|---------|
| `GIN_MODE` | 默认 debug 模式（启动有告警）| 设 `GIN_MODE=release` |
| CORS 跨域 | `cors.Default()` 允许 `*` 任意来源 | 限制为前端域名白名单 |
| 日志输出 | `fmt.Printf` 直接打到 stdout | 接结构化日志 + 文件 / 采集（logrus/zap）|
| 指标端口 | `METRICS_PORT` 默认 9090 监听全部网卡 | 仅供内网/VPC 访问，或前置鉴权；公网关闭（设 0 或防火墙禁止）|
| 传输安全 | 直接暴露 `:8080` HTTP | 放 Nginx 反向代理 + HTTPS/SSL |
| DSN 时区 | `loc=Local`（本地时区）| 生产统一为 `loc=UTC` 避免时区歧义 |

> 注：租户接口、文档接口等均已接入 JWT 并**移入私有路由组**强制鉴权，不再存在公开待办。

## 🔧 后续需补齐项（当前为快速开发 / 调试期留的口子）

> 以下均为项目当前状态下为了快速开发与方便调试**暂时留下的简化点**，需要在**第二周或面试前**补齐。粗体为优先级最高的底线项。

> 🎯 **需求单 0001「用户账号体系改进」已落地**（2026-08-12）：本清单第 1、2、3、4、11 条已收敛解决，详见下方各条 ⚠️ 标注。新增**公开注册租户 `POST /api/tenant/register`**，并配套端到端脚本 `scripts/e2e_account_register.sh`（**8 项全过**：注册租户→新 admin 登录→乱填 tenant_id 注册被拒→合法租户内建 admin→重复同名用户被拒→禁用租户登录被拒→未登录访问租户列表 401→注册租户审计落库）。

### 🔴 安全相关（必须改，面试必问）

1. **创建租户接口权限**
   - ⚠️ **已按需求单 0001 收敛**（2026-08-12 起）：原「任意登录用户（含 member）都能走私有 `POST /api/tenant` 建租户」的语义已废止。
   - 现状：新增**公开 `POST /api/tenant/register`**，任何未登录用户都可注册租户，注册成功**原子创建「租户 + 首个 admin(固定 role=admin) + 默认工具配置」**；原私有 `POST /api/tenant` 仅保留供管理员视角建租户（走同一事务方法）。「查租户列表/详情/改状态」仍保持私有受控，禁止外部枚举。
   - 安全底线已落地：① 建租户时首个 admin **固定 `role=admin`，调用方不可传 role，堵提权**；② 建租户 + 首个 admin + 工具配置**用 `storage.DB.Transaction` 包裹，任一步失败整体回滚**，不再留「无管理员的残缺租户」。
   - 后续：可再加人工审核 / 邀请制（当前按 demo 不引入超管角色）。

2. **用户注册接口权限**
   - ⚠️ **已按需求单 0001 收敛**（2026-08-12 起）：公开 `POST /api/user/register` 现**必须校验租户存在**后才能注册，杜绝「凭空编造不存在的 tenant_id 造孤儿账号 / 冒充真实公司」。
   - 现状：`service.Register` 进入后先 `storage.GetTenantByID` 校验租户存在，不存在返回明确「租户/公司不存在」，**不落库**；`role` 虽保留 `admin/member` 入参，但 **建租户入口已固定首个账号为 admin 且不可提权**，普通自助注册仍一律 member。
   - 后续：是否收紧为「租户管理员创建员工 / 邀请制」按需演进（当前保留公开自助注册）。

3. **租户 ID 传递安全（最高优先级）**
   - ⚠️ **已落地并硬性验证，且注册/登录口子已补齐**（2026-08-12 起）：
   - 私有接口侧：JWT 中间件已将 `tenant_id` 注入 Context，提供 `GetTenantID` 工具；文档/检索/向量化全链路强制从 Context 拿 `tenant_id`；`storage.SearchVectors` 签名强制 `tenantID` 参数 + 内部 filter 强过滤（不传编译不过）。
   - **公开接口侧（唯一信任前端自报 tenant_id 的两个口子）**：`user/register` 校验租户存在（不存在拒绝）；`user/login` 校验用户所属租户**存在且 `Status==1`**（孤儿账号 / 禁用租户内的账号均拒绝登录）。
   - 已验证：**多租户隔离硬性对抗测试通过**（见周六问题小节第 10 条）+ 需求单 0001 新增的「注册不存在租户被拒 / 禁用租户登录被拒 / 注册租户原子回滚」单测与端到端全过。

4. **密码强度校验**
   - 现状：注册时**保留长度校验**（`admin_password`/`password` 校验 `min=6,max=128` 等），未做复杂度强度策略（需求单 0001 明确范围外）
   - 后续：可增加密码复杂度校验（大小写、数字、符号组合）

### 🟡 工程完善（建议改，体现工程化）

5. **统一错误码**
   - 现状：**已有基础错误码常量但未细分** — `response.go` 定义了 `CodeSuccess/CodeBadRequest/CodeUnauthorized/CodeForbidden/CodeServerError`（0/400/401/403/500）
   - 问题：错误码粒度较粗，业务细分不够
   - 后续：进一步定义细粒度错误码，如 `40001`=参数错误、`40101`=未登录、`40301`=无权限、`50001`=服务器错误，所有接口统一使用

6. **请求日志中间件**
   - 现状：**已升级为结构化日志** ✅ — `middleware.Logger` 全局挂载，输出 JSON（方法/路径/状态码/耗时 + `fields_trace_id/tenant_id/user_id/latency`），统一走 `observability` 出口
   - 问题：目前结构化 JSON 打到 stdout，**未接文件 / 采集后端**（可用任意 JSON 采集器如 Loki/ELK 直接采集）
   - 后续：接入日志采集后端 + 落盘 / 滚动，即可完整可观测

7. **CORS 中间件**
   - 现状：**已有 `cors.Default()`** 全局中间件，能跨域，但允许任意来源
   - 问题：开发期够用，线上会暴露任意来源
   - 后续：改造为**受控 CORS**，允许前端域名白名单跨域，其余来源拒绝

8. **参数校验不统一**
   - 现状：**已统一** — 引入 `api/validator` 包（go-playground/validator v10），所有 JSON 请求结构体加 `binding` 校验标签（required/min/max/oneof 等），handler 统一走 `validator.BindJSON` + `validator.HandleBindError`；分页 query 参数也加了 min/max 标签
   - **统一校验错误处理**：校验失败返回**结构化字段错误**——`{"code":400,"message":"参数校验失败","data":{"username":"该字段为必填项，不能为空","password":"长度不能少于 6 个字符"}}`，字段 key 用的是 json/form 标签名，错误消息为中文提示（区分字符串长度与数值 min/max、oneof 取值、required 必填等），前端可精确定位错误字段
   - 其它绑定失败（空体 / JSON 格式错）返回 400 通用提示
   - 已替换：user/tenant/chat/knowledge/session/tool_admin 各 handler 的手写 `if` 校验全部移除，改为标签式校验 + 统一出口

9. **migrate 工具配置重复**
   - 现状：migrate 工具自己读 `.env`，api 服务也读 `.env`
   - 后续：统一复用 `config.Load()`，不要到处复制配置加载代码

### 🟢 功能补充（时间充裕再加）

10. **操作审计日志**
    - 现状：**已落地** ✅ — `audit_logs` 表建好；`api/service/audit.go` 的 `RecordAuditLog` 工具函数已写 UploadDocument(上传/删除文档)、`UpdateToolEnabled`(修改工具配置)、`Login`(登录)、`CreateSession`(创建会话)、`Chat`(RAG问答) 5 处埋点 + **`RegisterTenant`(注册租户)** 需求单 0001 新增埋点。记录 operation/content + 从 ctx 提取 tenant_id/user_id/trace_id 落表
    - 单测：`api/service/session_test.go` 的 `TestCreateSessionAudit`（连真实 MySQL，验证创建会话埋点 tenant/user/trace 落库）+ 端到端脚本 `scripts/e2e_user_journey.sh` 第 11 步覆盖 登录/上传文档/创建会话/RAG问答 四类审计，并校验 RAG 问答 content 记录了命中工具
    - ⚠️ 需求单 0001 补充：公开注册租户同样落审计（`operation='注册租户'`，公开接口无当前操作者，用新建的租户 ID + 管理员 ID 补齐 tenant_id/user_id，trace_id 沿用请求级），`.data` 返回的 admin/token 保证可查该笔审计
    - 后续：可再增至更多操作（删除会话、用量查询等）；并提供管理端审计日志查询接口

11. **租户状态未拦截**
    - ⚠️ **已按需求单 0001 解决**（2026-08-12 起）：`service.Login` 登录时校验用户所属租户**存在且 `Status==1`**，禁用租户 / 孤儿租户（不存在）内的账号一律拒绝登录，返回明确「该租户已被禁用/login 被拒」。
    - 后续：可选在 JWT 中间件也校验租户状态（当前登录态下发后租户状态变更需等 token 过期，属可接受取舍）

12. **无刷新 token 机制**
    - 现状：`access_token` 过期就得重新登录
    - 后续：增加 `refresh_token`，用其换取新 `access_token`，不必每次都输密码

### 🎯 优先级排序（第二周 / 面试前按此顺序补）

| 优先级 | 事项 |
|--------|------|
| 🔴 最高 | **`tenant_id` 全部从 JWT 拿，不信前端** → 多租户安全底线 |
| 🔴 高 | 统一错误码、参数校验 → 工程化体现 |
| 🟡 中 | 请求日志、CORS → 完善度 |
| 🟢 低 | 刷新 token → 锦上添花（审计日志已于可观测阶段落地）|

