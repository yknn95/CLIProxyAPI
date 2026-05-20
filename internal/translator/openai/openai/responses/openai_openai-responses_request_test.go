package responses

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

func prettyJSONForTest(raw []byte) string {
	if !gjson.ValidBytes(raw) {
		return string(raw)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw)
	}
	return out.String()
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_MergeConsecutiveFunctionCalls(t *testing.T) {
	raw := []byte(`{
		"input": [
			{"type":"function_call","call_id":"exec_command:0","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"},
			{"type":"function_call","call_id":"exec_command:1","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"},
			{"type":"function_call_output","call_id":"exec_command:0","output":"ok0"},
			{"type":"function_call_output","call_id":"exec_command:1","output":"ok1"}
		]
	}`)
	t.Logf("input json:\n%s", prettyJSONForTest(raw))

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("kimi-k2.6", raw, true)
	t.Logf("output json:\n%s", prettyJSONForTest(out))

	msgs := gjson.GetBytes(out, "messages")
	if !msgs.Exists() || !msgs.IsArray() {
		t.Fatalf("messages should be an array")
	}
	if got := len(msgs.Array()); got != 3 {
		t.Fatalf("messages count = %d, want %d", got, 3)
	}

	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "assistant" {
		t.Fatalf("messages.0.role = %q, want %q", got, "assistant")
	}
	if got := len(gjson.GetBytes(out, "messages.0.tool_calls").Array()); got != 2 {
		t.Fatalf("messages.0.tool_calls length = %d, want %d", got, 2)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.id").String(); got != "exec_command:0" {
		t.Fatalf("messages.0.tool_calls.0.id = %q, want %q", got, "exec_command:0")
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.1.id").String(); got != "exec_command:1" {
		t.Fatalf("messages.0.tool_calls.1.id = %q, want %q", got, "exec_command:1")
	}

	if got := gjson.GetBytes(out, "messages.1.tool_call_id").String(); got != "exec_command:0" {
		t.Fatalf("messages.1.tool_call_id = %q, want %q", got, "exec_command:0")
	}
	if got := gjson.GetBytes(out, "messages.2.tool_call_id").String(); got != "exec_command:1" {
		t.Fatalf("messages.2.tool_call_id = %q, want %q", got, "exec_command:1")
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_SplitFunctionCallsWhenInterrupted(t *testing.T) {
	raw := []byte(`{
		"input": [
			{"type":"function_call","call_id":"call_a","name":"tool_a","arguments":"{}"},
			{"type":"message","role":"user","content":"next"},
			{"type":"function_call","call_id":"call_b","name":"tool_b","arguments":"{}"}
		]
	}`)
	t.Logf("input json:\n%s", prettyJSONForTest(raw))

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("kimi-k2.6", raw, false)
	t.Logf("output json:\n%s", prettyJSONForTest(out))

	if got := len(gjson.GetBytes(out, "messages").Array()); got != 3 {
		t.Fatalf("messages count = %d, want %d", got, 3)
	}
	if got := gjson.GetBytes(out, "messages.0.tool_calls.0.id").String(); got != "call_a" {
		t.Fatalf("messages.0.tool_calls.0.id = %q, want %q", got, "call_a")
	}
	if got := gjson.GetBytes(out, "messages.2.tool_calls.0.id").String(); got != "call_b" {
		t.Fatalf("messages.2.tool_calls.0.id = %q, want %q", got, "call_b")
	}
}

func TestConvertOpenAIResponsesRequestToOpenAIChatCompletions_DefersMessageUntilToolOutput(t *testing.T) {
	raw := []byte(`{
		"input": [
			{"type":"function_call","call_id":"call_x","name":"exec_command","arguments":"{\"cmd\":\"echo hi\"}"},
			{"type":"message","role":"user","content":"Approved command prefix saved"},
			{"type":"function_call_output","call_id":"call_x","output":"ok"},
			{"type":"message","role":"user","content":"next"}
		]
	}`)
	t.Logf("input json:\n%s", prettyJSONForTest(raw))

	out := ConvertOpenAIResponsesRequestToOpenAIChatCompletions("kimi-k2.6", raw, true)
	t.Logf("output json:\n%s", prettyJSONForTest(out))

	if got := len(gjson.GetBytes(out, "messages").Array()); got != 4 {
		t.Fatalf("messages count = %d, want %d", got, 4)
	}
	if got := gjson.GetBytes(out, "messages.0.role").String(); got != "assistant" {
		t.Fatalf("messages.0.role = %q, want %q", got, "assistant")
	}
	if got := gjson.GetBytes(out, "messages.1.role").String(); got != "tool" {
		t.Fatalf("messages.1.role = %q, want %q", got, "tool")
	}
	if got := gjson.GetBytes(out, "messages.1.tool_call_id").String(); got != "call_x" {
		t.Fatalf("messages.1.tool_call_id = %q, want %q", got, "call_x")
	}
	if got := gjson.GetBytes(out, "messages.2.role").String(); got != "user" {
		t.Fatalf("messages.2.role = %q, want %q", got, "user")
	}
	if got := gjson.GetBytes(out, "messages.2.content").String(); got != "Approved command prefix saved" {
		t.Fatalf("messages.2.content = %q, want %q", got, "Approved command prefix saved")
	}
	if got := gjson.GetBytes(out, "messages.3.content").String(); got != "next" {
		t.Fatalf("messages.3.content = %q, want %q", got, "next")
	}
}

func TestConvertOpenAIChatCompletionsRequestToOpenAIResponses_PreservesInstructionsAndTools(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.4",
		"messages":[
			{"role":"system","content":"system rules"},
			{"role":"developer","content":"developer notes"},
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}]},
			{"role":"assistant","content":"let me inspect","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"done"}
		],
		"tools":[{"type":"function","function":{"name":"read","description":"Read file","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"read"}},
		"max_tokens":123,
		"parallel_tool_calls":true,
		"user":"user-1"
	}`)

	out := ConvertOpenAIChatCompletionsRequestToOpenAIResponses("gpt-5.4", raw, true)

	if got := gjson.GetBytes(out, "instructions").String(); got != "system rules\n\ndeveloper notes" {
		t.Fatalf("instructions = %q", got)
	}
	if got := gjson.GetBytes(out, "max_output_tokens").Int(); got != 123 {
		t.Fatalf("max_output_tokens = %d, want 123", got)
	}
	if !gjson.GetBytes(out, "parallel_tool_calls").Bool() {
		t.Fatal("parallel_tool_calls should be true")
	}
	if got := gjson.GetBytes(out, "metadata.user_id").String(); got != "user-1" {
		t.Fatalf("metadata.user_id = %q, want user-1", got)
	}
	if got := gjson.GetBytes(out, "input.0.role").String(); got != "user" {
		t.Fatalf("input.0.role = %q, want user", got)
	}
	if got := gjson.GetBytes(out, "input.0.content.0.type").String(); got != "input_text" {
		t.Fatalf("input.0.content.0.type = %q, want input_text", got)
	}
	if got := gjson.GetBytes(out, "input.0.content.1.type").String(); got != "input_image" {
		t.Fatalf("input.0.content.1.type = %q, want input_image", got)
	}
	if got := gjson.GetBytes(out, "input.1.role").String(); got != "assistant" {
		t.Fatalf("input.1.role = %q, want assistant", got)
	}
	if got := gjson.GetBytes(out, "input.2.type").String(); got != "function_call" {
		t.Fatalf("input.2.type = %q, want function_call", got)
	}
	if got := gjson.GetBytes(out, "input.2.call_id").String(); got != "call_1" {
		t.Fatalf("input.2.call_id = %q, want call_1", got)
	}
	if got := gjson.GetBytes(out, "input.3.type").String(); got != "function_call_output" {
		t.Fatalf("input.3.type = %q, want function_call_output", got)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "read" {
		t.Fatalf("tools.0.name = %q, want read", got)
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "read" {
		t.Fatalf("tool_choice.name = %q, want read", got)
	}
}
