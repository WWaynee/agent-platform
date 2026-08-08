package engine

import (
	"context"
	"testing"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// fakeLLM 模拟 LLM 客户端：用于测试引擎构造，不含真实业务。
type fakeLLM struct{}

func (fakeLLM) Chat(ctx context.Context, req ChatRequest) (string, error) {
	return "", nil
}

func TestNewReActEngine(t *testing.T) {
	llm := fakeLLM{}
	tools := toolmanager.NewToolManager()
	mem := memory.NewInMemoryMemory()

	e := NewReActEngine(llm, tools, mem, "你是一个助手")
	if e == nil {
		t.Fatal("构造应返回非 nil 引擎")
	}

	// 三个组件应正确持有
	if e.LLMClient == nil {
		t.Fatal("应持有 LLMClient")
	}
	if e.ToolManager != tools {
		t.Fatal("应持有传入的 ToolManager")
	}
	if e.Memory != mem {
		t.Fatal("应持有传入的 Memory")
	}

	// 默认最大迭代轮次为 5
	if e.MaxIterations != 5 {
		t.Fatalf("默认 MaxIterations 应为 5，got %d", e.MaxIterations)
	}

	// SystemPrompt 正确透传
	if e.SystemPrompt != "你是一个助手" {
		t.Fatalf("SystemPrompt 透传不符: %q", e.SystemPrompt)
	}

	// 允许覆盖最大迭代轮次
	e.MaxIterations = 10
	if e.MaxIterations != 10 {
		t.Fatal("MaxIterations 应可调整")
	}
}

func TestNewReActEngine_NilChecks(t *testing.T) {
	tools := toolmanager.NewToolManager()
	mem := memory.NewInMemoryMemory()

	// 三个组件任一为 nil 都应 panic（提前暴露错误配置）
	for name, fn := range map[string]func(){
		"nil llm":   func() { NewReActEngine(nil, tools, mem, "") },
		"nil tools": func() { NewReActEngine(fakeLLM{}, nil, mem, "") },
		"nil mem":   func() { NewReActEngine(fakeLLM{}, tools, nil, "") },
	} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("%s: 期望 panic 但未触发", name)
				}
			}()
			fn()
		}()
	}
}
