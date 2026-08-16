package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"agent-platform/config"
	"agent-platform/llmclient"
	"agent-platform/observability"
	"agent-platform/splitter"
	"agent-platform/storage"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ============ Service 层：文档文本切片（Chunking） ============
//
// 作用：把整篇文档文本切成长度合适、语义相对完整的片段，供后续 Embedding + 向量检索。
// 切片质量是 RAG 检索效果的基础：太粗丢精度，太碎断语义。
//
// 策略（已确定，见 splitter 包）：
//   - ChunkSize=600 字符、OverlapSize=80 字符，作为切分与重叠的参数。
//   - 按段落优先：短段落整体成块；长段落再按字符硬切。
//   - 相邻切片保留重叠，保证被切断的语义在至少一个切片里保持连续。
//
// 说明：本项目亮点在架构，切片采用"基础按字符 + 重叠"的简单策略，不过度追求语义分块精度。

// SplitText 把完整文档文本切成切片数组。
// 入参：text 完整文本
// 返回：切片后的字符串数组（空文本返回 nil）
//
// 实现分三步：
//  1. splitByParagraph 按换行分成段落，去空行；
//  2. 超长段落用 hardSplit 按 ChunkSize 硬切，得到 ≤ChunkSize 的 blocks；
//  3. 贪心拼接 blocks 成最终切片，切片之间保留 OverlapSize 重叠。
func SplitText(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	// 2. 得到基础块：把超长段落硬切成 ≤ChunkSize
	blocks := buildBlocks(text)

	// 3. 贪心拼接 blocks 成切片，保留重叠
	return assembleChunks(blocks)
}

// buildBlocks 把文本切成一系列"长度≤ChunkSize 的基础块"。
// 内部先按段落切，再对超长段落硬切；返回的基础块是后续拼接的最小语义单元。
func buildBlocks(text string) []string {
	var blocks []string
	for _, para := range paragraphize(text) {
		if len([]rune(para)) <= splitter.ChunkSize {
			blocks = append(blocks, para)
		} else {
			blocks = append(blocks, hardSplit(para, splitter.ChunkSize)...)
		}
	}
	return blocks
}

// assembleChunks 把基础块顺序拼成最终切片，相邻切片保留重叠。
// 每个切片长度 ≤ ChunkSize（在即将超限前封片）。
func assembleChunks(blocks []string) []string {
	var chunks []string
	var cur []rune

	for _, b := range blocks {
		bRunes := []rune(b)
		// 需要判断"当前片 + 分隔符 + 新块"是否超过上限：超了则封片开新片
		needSplit := len(cur) > 0 && len(cur)+1+len(bRunes) > splitter.ChunkSize
		if needSplit {
			// 封当前片
			chunks = append(chunks, string(cur))
			// 新片开头带上上一片尾部 OverlapSize 字符（重叠，保证语义连续）
			cur = overlapTail(cur)
		}

		// 拼接：若当前片非空，用换行分隔上一块与新块
		if len(cur) > 0 {
			cur = append(cur, '\n')
		}
		cur = append(cur, bRunes...)
	}
	if len(cur) > 0 {
		chunks = append(chunks, string(cur))
	}
	return chunks
}

// overlapTail 截取一片末尾 OverlapSize 字符作为重叠前缀。
// 若片本身不足 OverlapSize，则整段作为重叠（此时相当于无重叠损失）。
func overlapTail(prev []rune) []rune {
	start := len(prev) - splitter.OverlapSize
	if start < 0 {
		start = 0
	}
	out := make([]rune, len(prev)-start)
	copy(out, prev[start:])
	return out
}

// paragraphize 按换行分割文本成段落，去掉空行（保留段落内原有换行？此处按行拆分，空行丢弃）。
func paragraphize(text string) []string {
	lines := strings.Split(text, "\n")
	var paragraphs []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		paragraphs = append(paragraphs, ln)
	}
	return paragraphs
}

// hardSplit 把超长段落按 size（字符数）硬切成若干 ≤size 的切片。
// 用 rune 处理，确保中文等宽字符不会被切成半个。
func hardSplit(para string, size int) []string {
	rs := []rune(para)
	if len(rs) <= size {
		return []string{para}
	}
	var out []string
	for start := 0; start < len(rs); start += size {
		end := start + size
		if end > len(rs) {
			end = len(rs)
		}
		out = append(out, string(rs[start:end]))
	}
	return out
}

// ============ 文档文件读取 ============

// ReadTextDocument 从 MinIO 下载文档并读取为纯文本。
// 入参：minioKey 对象在 MinIO 内的存储路径、filename 原始文件名（用于判断扩展名）
// 返回：文件文本内容
//
// 当前仅支持 .txt / .md（纯文本格式，直接按 UTF-8 读取）。
// 暂不支持 PDF —— PDF 需额外解析库（当前先打通 txt/md 链路，PDF 后补）。
// 扩展名判断失败（如 .pdf）返回明确错误，避免静默生成无意义文本。
func ReadTextDocument(minioKey, filename string) (string, error) {
	// 1. 校验扩展名：仅支持 txt / md
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md":
		// 支持
	default:
		return "", fmt.Errorf("暂不支持的文件类型 %q，当前仅支持 .txt/.md", ext)
	}

	// 2. 从 MinIO 下载文件内容
	data, err := storage.DownloadFile(minioKey)
	if err != nil {
		return "", err
	}

	// 3. 转为 UTF-8 文本（txt/md 均为纯文本）
	return string(data), nil
}

// ============ 文档向量化主流程 ============

// embedBatchSize 单次 Embedding 批量请求携带的切片数。
// 批量请求比逐条请求省大量 HTTP 往返；取 16 平衡单次请求体大小与并发效率。
const embedBatchSize = 16

// ErrDocumentNotFound 文档不存在或不属于当前租户时的哨兵错误。
// handler 层可用 errors.Is 识别它，对"资源不存在"返回 4xx 而非 5xx。
var ErrDocumentNotFound = errors.New("文档不存在或无权访问")

// ProcessDocument 文档向量化主流程：上传的文档 → 切块 → 向量化 → 写入 Qdrant。
//
// 完整流程（任务给出的编排）：
//  1. 从数据库查文档，取 MinIO object key
//  2. 从 MinIO 下载文件并读取文本
//  3. SplitText 切成片段
//  4. 遍历所有切片，批量调用 Embedding 生成向量（embedBatchSize 一批，避免逐条调用）
//  5. 批量写入 Qdrant 向量库（UpsertVectors 一次批量 upsert 全部点）
//  6. 更新 document 状态为 success
//
// ⚠️ 状态流转（任务要求）：
//   - 步骤 1.5 后立即置为 processing（标记开始处理，供前端轮询/观察）
//   - 任一环节出错 → 改为 failed，并把错误信息记录到文档的 error_msg 字段
//   - 成功 → success，error_msg 清空
//   - 文档不存在（第 1 步就查不到）→ 直接返回 ErrDocumentNotFound，不置 failed
//     （文档本身不存在，置 failed 无意义且误导排查）
//
// ⚠️ 多租户安全：整个流程只按 tenantID + documentID 操作，写入 Qdrant 的点
// 都携带 tenant_id（QdrantVector.TenantID），保证检索时按租户隔离。
//
// 幂等说明：点 ID 用 (documentID<<32 | chunkIndex) 合成，同一文档重复处理会覆盖同 ID 的点，
// 不会产生重复向量。
func ProcessDocument(ctx context.Context, tenantID, documentID uint64) error {
	// 1. 查文档，拿 MinIO object key
	doc, err := storage.GetDocumentByID(ctx, tenantID, documentID)
	if err != nil {
		// 文档不存在（或不属于当前租户）：直接返回哨兵错误，不尝试置 failed。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDocumentNotFound
		}
		// 其他数据库异常：正常走失败处理，置 failed 并记录原因
		return failProcess(ctx, documentID, fmt.Errorf("查询文档失败: %w", err))
	}

	// 1.5 标记正在处理（processing），供前端轮询状态
	// 从此刻起若后续任何一步失败，都会把状态覆盖为 failed 并记录原因
	if err := storage.UpdateDocumentStatus(ctx, documentID, "processing"); err != nil {
		return failProcess(ctx, documentID, fmt.Errorf("更新文档为处理中失败: %w", err))
	}

	// 2. 下载并读取文本
	text, err := ReadTextDocument(doc.MinioObjectKey, doc.Name)
	if err != nil {
		return failProcess(ctx, documentID, fmt.Errorf("读取文档失败: %w", err))
	}

	// 3. 切片
	chunks := SplitText(text)
	if len(chunks) == 0 {
		return failProcess(ctx, documentID, fmt.Errorf("文档内容为空，无可切片文本"))
	}

	// 4. 分批 Embedding（一次请求一批，避免逐条调用浪费时间）
	//    用贯穿下来的 ctx（携带 trace_id/tenant_id），LLM 调用日志自动带链路/租户身份。
	llm := llmclient.NewClient(config.GlobalConfig.LLM)

	// 先为每片生成向量（顺序与 chunks 一一对应）
	vectors := make([][]float32, len(chunks))
	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[start:end]
		resp, err := llm.EmbedBatch(ctx, llmclient.EmbeddingBatchRequest{Inputs: batch})
		if err != nil {
			return failProcess(ctx, documentID, fmt.Errorf("批量向量化切片[%d:%d]失败: %w", start, end, err))
		}
		// 防御：返回向量数量必须等于本批切片数，否则向量与切片对不上，写入会错位
		if len(resp.Vectors) != len(batch) {
			return failProcess(ctx, documentID, fmt.Errorf(
				"批量向量化切片[%d:%d]数量不符: 请求 %d 条，返回 %d 条", start, end, len(batch), len(resp.Vectors)))
		}
		copy(vectors[start:end], resp.Vectors)
	}

	// 5. 组装 Qdrant 点并批量写入
	points := make([]storage.QdrantVector, 0, len(chunks))
	for i, chunk := range chunks {
		points = append(points, storage.QdrantVector{
			ID:           composePointID(documentID, i), // 点全局唯一
			TenantID:     tenantID,
			DocumentID:   documentID,
			DocumentName: doc.Name, // 文档名：检索命中时随结果返回，LLM 据此引用来源
			ChunkIndex:   i,
			Content:      chunk,
			Vector:       vectors[i],
		})
	}
	if err := storage.UpsertVectors(ctx, points); err != nil {
		return failProcess(ctx, documentID, fmt.Errorf("批量写入向量库失败: %w", err))
	}

	// 6. 更新状态为 success
	if err := storage.UpdateDocumentResult(ctx, documentID, "success", ""); err != nil {
		return fmt.Errorf("更新文档状态失败: %w", err)
	}

	// 7.（可选项）成功后再预生成文档摘要落库，供 list_documents 帮 LLM 选文档。
	//    尽力而为：生成/落库任何一步失败都只记日志，不改变主流程的 success 状态。
	generateAndStoreSummary(ctx, tenantID, documentID, doc.Name, text)

	return nil
}

// ============ 文档摘要预生成（可选项：阶段2(6)） ============

// summaryMaxChars 摘要最长字符数。摘要主要用于 list_documents 帮 LLM 快速判断文档内容价值，
// 过长反而占用上下文；取 300 字符足够承载 3~5 句核心摘要。
const summaryMaxChars = 300

// summaryInputChars 送入 LLM 用于生成摘要的正文上限。
// 生成摘要只需把握文档主题与关键内容，不必读全文；取正文前 6000 字符足够，
// 避免超大文档把大量 token 花在摘要这一步。
const summaryInputChars = 6000

// generateAndStoreSummary 向量化成功后调用 LLM 为文档生成摘要并落库 documents.summary。
// 设计要点：
//   - 仅取正文前 summaryInputChars 字符喂给 LLM，控制摘要成本；
//   - 摘要结果按 summaryMaxChars 截断，避免过长；
//   - 全程"尽力而为"：Chat 或落库失败只记 error 日志并直接返回，绝不阻塞向量化主流程的 success 状态。
//
// 日志规范：用 observability.WithContext(ctx) 取带 trace_id/tenant_id 的 logger，
// 错误走 logger.Error(msg, err, ...)，耗时字段统一用 observability.FieldLatency。
func generateAndStoreSummary(ctx context.Context, tenantID, documentID uint64, docName, text string) {
	logger := observability.WithContext(ctx)
	start := time.Now()

	// 只取正文前 summaryInputChars 字符喂给 LLM（中文按 rune 截，不切半个字）
	src := []rune(text)
	if len(src) > summaryInputChars {
		src = src[:summaryInputChars]
	}
	srcRunes := string(src)

	llm := llmclient.NewClient(config.GlobalConfig.LLM)
	resp, err := llm.Chat(ctx, llmclient.ChatRequest{
		Messages: []llmclient.ChatMessage{
			{Role: llmclient.RoleSystem, Content: "你是一个为企业知识库文档生成摘要的助手。基于提供的文档正文，用简洁通顺的中文生成 3~5 句核心摘要，涵盖文档主题、主要内容与用途。只输出摘要本身，不要标题、不要多余解释。"},
			{Role: llmclient.RoleUser, Content: fmt.Sprintf("文档名称：《%s》\n文档正文（摘要片段）：\n%s", docName, srcRunes)},
		},
		Temperature: 0.3,
	})
	if err != nil {
		logger.Error("文档摘要生成失败",
			zap.Uint64("document_id", documentID),
			zap.Error(err))
		return
	}

	summary := strings.TrimSpace(resp.Content)
	// 空摘要或纯空白视为生成失败，静默返回（不落库）
	if summary == "" {
		logger.Warn("文档摘要为空，跳过落库", zap.Uint64("document_id", documentID))
		return
	}
	// 按 summaryMaxChars 截断，避免摘要过长占上下文
	if rs := []rune(summary); len(rs) > summaryMaxChars {
		summary = string(rs[:summaryMaxChars])
	}

	// 带租户过滤落库（防跨租户覆盖），失败只记日志不阻塞主流程
	if err := storage.UpdateDocumentSummary(ctx, tenantID, documentID, summary); err != nil {
		logger.Error("文档摘要落库失败",
			zap.Uint64("document_id", documentID),
			zap.Error(err))
		return
	}

	logger.Info("文档摘要已生成",
		zap.Uint64("document_id", documentID),
		zap.Int("summary_chars", len([]rune(summary))),
		zap.Int64(observability.FieldLatency, time.Since(start).Milliseconds()),
	)
}

// composePointID 合成一个向量点的全局唯一 ID。
// 用 (documentID<<32 | chunkIndex) 拼进一个 uint64：
//   - 高 32 位是文档 ID，低 32 位是切片序号；
//   - 保证不同文档、不同切片 ID 不冲突；同一文档重复处理时同一切片 ID 相同 → 覆盖幂等。
func composePointID(documentID uint64, chunkIndex int) uint64 {
	return (documentID << 32) | uint64(chunkIndex)
}

// failProcess 统一处理失败：把文档状态置为 failed 并记录错误信息，返回原始错误。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使状态落库日志带同一链路 ID。
// 说明：状态落库失败时，仍返回原始错误（以主流程错误为准）；仅打印落库错误供排查。
func failProcess(ctx context.Context, documentID uint64, processErr error) error {
	if uerr := storage.UpdateDocumentResult(ctx, documentID, "failed", processErr.Error()); uerr != nil {
		// 连状态都更新不上属于严重问题，返回组合错误便于定位
		return fmt.Errorf("%v；此外更新失败状态时报错: %w", processErr, uerr)
	}
	return processErr
}
