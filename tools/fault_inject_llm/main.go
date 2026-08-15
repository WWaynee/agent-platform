package main

// =============================================================================
// FaultInjectLLM —— LLM 故障注入 mock 服务器
// -----------------------------------------------------------------------------
// 用于"LLM 接口故障测试"：模拟真实 LLM 服务在不同故障下的行为，
// 供 agent-platform 服务在故障场景下做端到端联调验证（不依赖真实外部模型）。
//
// 启动： go run ./tools/fault_inject_llm -port 18333
// 然后配置服务 LLM_BASE_URL=http://127.0.0.1:18333 启动 api，
// 服务对 /chat/completions、/embeddings 的调用会命中本 mock。
//
// 通过 control 接口在运行时切换故障模式（无需重启服务）：
//   PUT /mode {"mode":"ok|timeout|fail500|fail500_recover|garbage"}
//       - ok:              正常返回（默认）
//       - timeout:         挂起超过慢阈值（默认 3000ms），模拟请求超时
//       - fail500:         一律返回 HTTP 500，模拟服务端故障
//       - fail500_recover: 前 5 次返回 500，之后恢复正常（测熔断触发→半开→恢复）
//       - garbage:         返回非合法 JSON 的畸形内容（测解析降级/重试）
//   GET  /mode            查看当前模式
//   GET  /stats           各模式命中次数 + chat/embed 总计数
//   PUT  /slow {"ms":N}   设置 timeout 模式的挂起时长（毫秒）
//
// 计数供联调脚本断言"重试次数 / 熔断试探次数"。
// =============================================================================

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	port         = flag.Int("port", 18333, "监听端口")
	slowMs int64 = 3000
	stats  Stats
	respC  = make(chan string, 1) // 当前模式的串行读写信道
)

// Stats 请求统计（并发下每个计数用原子累加）
type Stats struct {
	ChatTotal   int64 `json:"chat_total"`
	EmbedTotal  int64 `json:"embed_total"`
	OkHits      int64 `json:"ok_hits"`
	TimeoutHits int64 `json:"timeout_hits"`
	Fail500Hits int64 `json:"fail500_hits"`
	RecoverHits int64 `json:"recover_hits"`
	RecoverKept int64 `json:"recover_kept"` // fail500_recover 返回 500 的次数
	GarbageHits int64 `json:"garbage_hits"`
}

func init() { respC <- "ok" } // 初始模式 ok

func getMode() string  { m := <-respC; respC <- m; return m }
func setMode(m string) { <-respC; respC <- m }

func main() {
	flag.Parse()

	mux := http.NewServeMux()

	// OpenAI 兼容对话端点（LLM 客户端调用 {BaseURL}/chat/completions）
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&stats.ChatTotal, 1)
		// 读 body 供 ok 模式做"知识问答式"(ReAct 驱动)应答：
		// 让引擎能走通 提问→knowledge_retrieve→观察→final_answer，便于端到端（尤其并发隔离）验证。
		body, _ := io.ReadAll(r.Body)
		switch getMode() {
		case "timeout":
			atomic.AddInt64(&stats.TimeoutHits, 1)
			time.Sleep(time.Duration(atomic.LoadInt64(&slowMs)) * time.Millisecond)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"upstream timeout"}`))
		case "fail500":
			atomic.AddInt64(&stats.Fail500Hits, 1)
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
		case "fail500_recover":
			atomic.AddInt64(&stats.RecoverHits, 1)
			if atomic.AddInt64(&stats.RecoverKept, 1) <= 5 {
				http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
				return
			}
			okResponse(w)
		case "garbage":
			atomic.AddInt64(&stats.GarbageHits, 1)
			// 返回非合法 JSON 的纯文本（无法解析出 choices/content）
			_, _ = w.Write([]byte(`这是一段不是 JSON 的纯文本，没有任何对象结构，只是为了测试格式错误的降级处理究竟会怎样走`))
		default: // ok
			atomic.AddInt64(&stats.OkHits, 1)
			okReActResponse(w, body) // 基于请求内容做知识问答式应答（ReAct 驱动）
		}
	})

	// OpenAI 兼容向量端点
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&stats.EmbedTotal, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3,0.4,0.5]}],"usage":{"prompt_tokens":8,"total_tokens":8}}`))
	})

	// control：查看/切换模式
	mux.HandleFunc("/mode", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"mode": getMode()})
		case http.MethodPut:
			defer r.Body.Close()
			var b struct {
				Mode string `json:"mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
				return
			}
			switch b.Mode {
			case "ok", "timeout", "fail500", "fail500_recover", "garbage":
				if b.Mode == "fail500_recover" {
					// 每次切到 recover 模式都重置"已返回500次数"，
					// 保证总是"前 N 次失败、之后成功"，便于脚本反复测熔断触发与恢复。
					atomic.StoreInt64(&stats.RecoverKept, 0)
				}
				setMode(b.Mode)
			default:
				http.Error(w, "unknown mode: "+b.Mode, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"mode": getMode()})
		default:
			http.Error(w, "GET/PUT only", http.StatusMethodNotAllowed)
		}
	})

	// control：设置 timeout 模式挂起时长
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "PUT only", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var b struct {
			Ms int64 `json:"ms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Ms <= 0 {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		atomic.StoreInt64(&slowMs, b.Ms)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"slow_ms": b.Ms})
	})

	// control：统计
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stats)
	})

	addr := ":" + strconv.Itoa(*port)
	fmt.Printf("[fault-inject-llm] listening on %s, mode=ok, timeout=%dms\n", addr, slowMs)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, "[fault-inject-llm] server error:", err)
		os.Exit(1)
	}
}

// okResponse 返回正常的 OpenAI 兼容响应
func okResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(
		`{"choices":[{"message":{"content":"故障注入正常响应 ok"}}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
}

// ================= 知识问答式(ReAct 驱动)应答 =================
// 让 mock 在 ok 模式具备"调用知识库工具→基于观察作答"的简单 ReAct 行为，
// 使端到端对话（尤其多租户并发隔离验证）能真实走通：
//
//	首次提问 → 返回 knowledge_retrieve 动作（触发知识库检索工具）
//	检索观察(观察里带"知识库检索…"字样) → 返回 final_answer，且【只引用观察内容】作答
//
// ⚠️ 隔离要点：mock 只依据引擎喂来的"知识库检索观察"内容作答，绝不凭自身知识编造；
// 租户 A 检索不到租户 B 的向量，观察里就没有 B 的内容 → answer 自然不含 B 的机密，
// 从 LLM 侧同样保证"搜不到对端向量、不越权"。

// chatBodyMock 只解析我们关心的 messages 字段（其余忽略）。
type chatBodyMock struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// okReActResponse 基于请求 messages 返回一个 ReAct 动作（OpenAI 兼容格式）。
func okReActResponse(w http.ResponseWriter, body []byte) {
	action, input := decideReActAction(body)
	payload := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"content": fmt.Sprintf(`{"action":%q,"action_input":%q}`, action, input),
				},
			},
		},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// decideReActAction 决定本轮 mock 应返回的动作：触发检索 or 基于观察作答 or 会话内记忆作答。
//
// 优先级：
//  1. 引擎喂回"知识库检索观察"(role:user) → 只基于观察内容作答（绝不编造 → 无越权泄漏）；
//  2. 本消息是"自我介绍"(我叫X) → 确认记住（名字会随消息进入会话历史，下轮可有记忆）；
//  3. 本消息是"询问名字" → 基于【本会话】历史里介绍过的名字作答（会话上下文隔离）；
//  4. 其它（真·知识查询）→ 触发 knowledge_retrieve 工具。
func decideReActAction(body []byte) (action, input string) {
	var req chatBodyMock
	if err := json.Unmarshal(body, &req); err != nil {
		return "final_answer", "请求解析失败，请换个问法。"
	}

	// 找到最后一条 user 消息（引擎把知识库检索观察以 role:user 喂回）
	lastUser := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			lastUser = m.Content
		}
	}
	if lastUser == "" {
		return "final_answer", "请提供要查询的内容。"
	}

	// 1) 已执行过知识检索 → 只基于观察结果作答
	if strings.Contains(lastUser, "知识库检索") {
		return "final_answer", answerFromObservation(lastUser)
	}

	// 1.5) 工具执行失败/未启用的观察 → 明确"工具不可用"，终止死循环并给友好提示。
	// 引擎在工具无权限（如管理员关闭该工具）时，会把 `工具 "X" 执行失败: ...无权限/未启用...`
	// 作为观察喂回 mock；若 mock 不识别就会再次请求该工具 → 死循环到 maxIterations 才"未收敛"。
	// 命中此观察 → 直接 final_answer，使"工具关闭后回答体现未启用语义"的命题成立。
	if strings.Contains(lastUser, "工具") &&
		(strings.Contains(lastUser, "执行失败") || strings.Contains(lastUser, "无权限") || strings.Contains(lastUser, "未启用")) {
		return "final_answer", "该工具当前未启用（已被管理员关闭），我无法从知识库获取相关资料。"
	}

	// 2) 本消息即自我介绍 → 确认记住（名字会写入会话历史，后续轮次可回忆）
	if local, ok := extractIntroducedName(lastUser); ok {
		return "final_answer", fmt.Sprintf("好的，已记住你叫%s。", local)
	}

	// 3) 询问名字 → 用"本会话"历史里介绍过的名字作答（会话上下文隔离的关键）
	if isAskingName(lastUser) {
		if name, ok := introducedNameFromHistory(req.Messages); ok {
			return "final_answer", fmt.Sprintf("根据本会话前的记忆，你叫%s。", name)
		}
		return "final_answer", "本会话中没有记录过你的名字。"
	}

	// 3.5) 通用常识问题（无企业知识指向）→ 直接作答、不触发知识库检索。
	// ⚠️ 真实 LLM 靠语义判断，mock 用关键词近似：命中明显非企业资料的通用常识词
	// 即直接 final_answer，使"常识问题不调用工具"这一命题在端到端链路成立；
	// 企业知识类问法（含"我们公司/内部/奖金/带薪"等）不命中，仍走知识检索。
	if isCommonSenseQuestion(lastUser) {
		return "final_answer", commonSenseAnswer(lastUser)
	}

	// 4) 其余（真·知识查询）→ 触发 knowledge_retrieve 工具
	runes := []rune(strings.TrimSpace(lastUser))
	if len(runes) > 60 {
		runes = runes[:60]
	}
	return "knowledge_retrieve", fmt.Sprintf(`{"query":%q}`, string(runes))
}

// commonSenseKeywords 用于近似识别"通用常识问题"的关键词（命中其一即直接作答）。
// 刻意只收录明显与租户企业资料无关的通用常识问法；企业知识类问法不在此列。
var commonSenseKeywords = []string{
	"离太阳", "地球", "太阳到", "有多远", "光年", "几岁", "首都是",
	"等于几", "最高的山", "最大的城市", "水的", "什么颜色", "味道是",
}

// isCommonSenseQuestion 判断最后一条 user 消息是否为通用常识问题。
func isCommonSenseQuestion(content string) bool {
	for _, kw := range commonSenseKeywords {
		if strings.Contains(content, kw) {
			return true
		}
	}
	return false
}

// commonSenseAnswer 为常识问题给出朴素答案（mock 近似，够端到端验证用）。
func commonSenseAnswer(content string) string {
	if strings.Contains(content, "离太阳") || strings.Contains(content, "太阳到") || strings.Contains(content, "有多远") {
		return "地球离太阳平均约 1.5 亿公里（1 个天文单位）。"
	}
	return "这是通用常识问题，无需查询企业知识库。"
}

// introducedNameFromHistory 在"本会话"的 messages 历史里找出最后一次自我介绍的名字。
// 引擎会把本会话（Redis 按 tenant+session 隔离存储）的历史消息整体拼进 messages，
// 因此这里读到的只有当前会话的历史 → 天然做到"会话之间上下文不相通、不串数据"。
// 工具观察（含"知识库检索"）不视作自我介绍，予以排除。
func introducedNameFromHistory(msgs []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) (string, bool) {
	name := ""
	found := false
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		if n, ok := extractIntroducedName(m.Content); ok {
			name, found = n, true
		}
	}
	return name, found
}

// extractIntroducedName 从一条 user 消息里提取"自我介绍"的名字。
// 命中模式：我的名字是X / 我名字是X / 名字叫X / 我叫作X / 我叫X。
// 疑问句（含"什么/？/多少"）与工具观察（含"知识库检索"）不视为自我介绍。
func extractIntroducedName(content string) (string, bool) {
	if content == "" || strings.Contains(content, "知识库检索") {
		return "", false
	}
	if strings.Contains(content, "什么") || strings.Contains(content, "？") ||
		strings.Contains(content, "多少") || strings.Contains(content, "是什么") {
		return "", false
	}
	kws := []string{"我的名字是", "我名字是", "名字叫", "我的名字叫", "我叫作", "我叫"}
	for _, kw := range kws {
		idx := strings.Index(content, kw)
		if idx < 0 {
			continue
		}
		// strings.Index 返回字节偏移，kw 的字节长度 len(kw) 与之配套
		rest := content[idx+len(kw):]
		var name []rune
		for _, r := range rest {
			if r == '。' || r == '，' || r == '！' || r == '？' || r == ',' ||
				r == '.' || r == '!' || r == ' ' || r == '\u3000' {
				break
			}
			name = append(name, r)
		}
		if len(name) >= 1 && len(name) <= 20 {
			return string(name), true
		}
	}
	return "", false
}

// isAskingName 判断一条 user 消息是否在"询问名字"。
func isAskingName(content string) bool {
	if content == "" || strings.Contains(content, "知识库检索") {
		return false
	}
	return strings.Contains(content, "我的名字叫什么") ||
		strings.Contains(content, "我叫什么") ||
		strings.Contains(content, "我名字") ||
		strings.Contains(content, "我的名字是什么")
}

// answerFromObservation 由观察内容组织 final_answer（只摘取/引述观察里的信息）。
// 空结果 → 如实说未找到，绝不编造。
func answerFromObservation(observation string) string {
	// 空结果的观察文案由 knowledge_retrieve 工具返回
	if strings.Contains(observation, "没有返回任何结果") || strings.Contains(observation, "未找到与问题相关") {
		return "知识库中没有检索到与这个问题相关的资料，我无法确认答案，请勿猜测。"
	}
	// 非空：截取观察里"序号-内容"之后的片段（即命中文档内容）作为回答依据，
	// 最多截断 200 字，避免超长。mock 只转述观察，天然不含对端租户任何内容。
	idx := strings.Index(observation, "文档ID=")
	if idx >= 0 {
		observation = observation[idx:]
	}
	runes := []rune(observation)
	const cap = 200
	if len(runes) > cap {
		runes = runes[:cap]
	}
	s := string(runes)
	s = strings.ReplaceAll(s, "\n", " ")
	return "根据检索到的内部资料：" + s
}
