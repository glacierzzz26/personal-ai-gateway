package translate

import (
	"encoding/json"
	"testing"
)

// —— buildO2ARequest:openai chat 请求 → anthropic /v1/messages 请求体 ——

func TestBuildO2ARequestToolsShape(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-5",
		"messages": [{"role": "user", "content": "weather?"}],
		"tools": [{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "w",
				"parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}
			}
		}]
	}`
	op, out, _, err := BuildRequest(ProtoOpenAI, ProtoAnthropic, OpChat, []byte(body), false)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if op != OpMessages {
		t.Fatalf("outOp = %q, want messages", op)
	}
	var req struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("tools len = %d\n%s", len(req.Tools), out)
	}
	tool := req.Tools[0]
	// anthropic 形状:name/description/input_schema 在顶层,无 openai 的 type/function 外壳
	if tool["name"] != "get_weather" || tool["description"] != "w" {
		t.Errorf("tool name/description = %v/%v", tool["name"], tool["description"])
	}
	if _, bad := tool["function"]; bad {
		t.Errorf("tool 不得带 openai 'function' 外壳: %v", tool)
	}
	if _, bad := tool["type"]; bad {
		t.Errorf("tool 不得带 openai 'type' 字段: %v", tool)
	}
	schema, ok := tool["input_schema"].(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Errorf("input_schema = %v, want the parameters object", tool["input_schema"])
	}
	if props, ok := schema["properties"].(map[string]any); !ok || props["city"] == nil {
		t.Errorf("input_schema.properties = %v", schema["properties"])
	}
}

func TestBuildO2ARequestToolChoiceNamed(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-5",
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{}}}}],
		"tool_choice": {"type": "function", "function": {"name": "get_weather"}}
	}`
	_, out, _, err := BuildRequest(ProtoOpenAI, ProtoAnthropic, OpChat, []byte(body), false)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	var req struct {
		ToolChoice map[string]any `json:"tool_choice"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.ToolChoice["type"] != "tool" || req.ToolChoice["name"] != "get_weather" {
		t.Errorf("tool_choice = %v, want {type:tool, name:get_weather}", req.ToolChoice)
	}
}
