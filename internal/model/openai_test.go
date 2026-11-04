package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleSendsBoundedToolRequestAndDecodesResponse(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var wire map[string]any
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if wire["model"] != "test-model" || wire["tool_choice"] != "auto" {
			t.Errorf("request body = %+v", wire)
		}
		tools, ok := wire["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Errorf("tools = %+v", wire["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{
				"role":"assistant",
				"content":null,
				"tool_calls":[{
					"id":"provider-call-1",
					"type":"function",
					"function":{"name":"termcp-core__term","arguments":"{\"cmd\":\"pwd\"}"}
				}]
			}}]
		}`))
	}))
	defer server.Close()

	adapter, err := NewOpenAICompatible(OpenAIConfig{
		Endpoint:        server.URL + "/v1/chat/completions",
		Model:           "test-model",
		APIToken:        token,
		MaxOutputTokens: 512,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible() error = %v", err)
	}
	response, err := adapter.Complete(context.Background(), Request{
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{Role: RoleUser, Content: "run pwd"},
		},
		Tools: []ToolDefinition{{
			Name: "termcp-core__term", Description: "run term", Parameters: json.RawMessage(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(response.Message.ToolCalls) != 1 ||
		response.Message.ToolCalls[0].Name != "termcp-core__term" ||
		string(response.Message.ToolCalls[0].Arguments) != `{"cmd":"pwd"}` {
		t.Fatalf("response = %+v", response)
	}
}

func TestOpenAICompatibleReturnsContentWithoutLeakingProviderBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"final answer"}}]}`))
	}))
	defer server.Close()
	adapter, err := NewOpenAICompatible(OpenAIConfig{
		Endpoint: server.URL + "/v1/chat/completions",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil || response.Message.Content != "final answer" {
		t.Fatalf("response = %+v, error = %v", response, err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "secret-provider-body", http.StatusUnauthorized)
	}))
	defer failing.Close()
	adapter, err = NewOpenAICompatible(OpenAIConfig{
		Endpoint: failing.URL + "/v1/chat/completions",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err == nil || strings.Contains(err.Error(), "secret-provider-body") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAICompatibleKeepsProviderReasoningTransient(t *testing.T) {
	const reasoning = "private provider reasoning"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var wire struct {
			Messages []openAIMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&wire); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(wire.Messages) != 1 || wire.Messages[0].ReasoningContent != reasoning {
			t.Errorf("request messages = %+v", wire.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{
				"role":"assistant",
				"content":"done",
				"reasoning_content":"private provider reasoning"
			}}]
		}`))
	}))
	defer server.Close()

	adapter, err := NewOpenAICompatible(OpenAIConfig{
		Endpoint: server.URL + "/v1/chat/completions",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Complete(context.Background(), Request{
		Messages: []Message{{
			Role:             RoleAssistant,
			ReasoningContent: reasoning,
			ToolCalls: []ToolCall{{
				ID: "call-1", Name: "tool", Arguments: json.RawMessage(`{}`),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.ReasoningContent != reasoning || response.Message.Content != "done" {
		t.Fatalf("response = %+v", response)
	}
	encoded, err := json.Marshal(response.Message)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), reasoning) {
		t.Fatalf("serialized model message leaked provider reasoning: %s", encoded)
	}
}

func TestOpenAICompatibleRejectsUnsafeEndpointsAndToolArguments(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com/v1/chat/completions",
		"https://user:pass@example.com/v1/chat/completions",
		"https://example.com/v1/chat/completions?token=secret",
		"https://example.com",
	} {
		if _, err := NewOpenAICompatible(OpenAIConfig{Endpoint: endpoint, Model: "test"}); err == nil {
			t.Fatalf("unsafe endpoint %q was accepted", endpoint)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":null,"tool_calls":[{"type":"function","function":{"name":"tool","arguments":"[]"}}]}}]}`))
	}))
	defer server.Close()
	adapter, err := NewOpenAICompatible(OpenAIConfig{
		Endpoint: server.URL + "/v1/chat/completions",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("non-object arguments error = %v", err)
	}
}

func TestOpenAICompatibleResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer server.Close()
	adapter, err := NewOpenAICompatible(OpenAIConfig{
		Endpoint:         server.URL + "/v1/chat/completions",
		Model:            "test-model",
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("response limit error = %v", err)
	}
}
