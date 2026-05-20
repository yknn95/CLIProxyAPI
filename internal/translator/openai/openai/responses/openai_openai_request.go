package responses

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertOpenAIChatCompletionsRequestToOpenAIResponses converts OpenAI chat completions
// requests into OpenAI responses format so providers can reuse the native responses path.
func ConvertOpenAIChatCompletionsRequestToOpenAIResponses(modelName string, inputRawJSON []byte, stream bool) []byte {
	root := gjson.ParseBytes(inputRawJSON)

	out := []byte(`{"model":"","input":[],"stream":false}`)
	out, _ = sjson.SetBytes(out, "model", modelName)
	out, _ = sjson.SetBytes(out, "stream", stream)

	if maxTokens := root.Get("max_tokens"); maxTokens.Exists() {
		out, _ = sjson.SetBytes(out, "max_output_tokens", maxTokens.Int())
	}
	if parallelToolCalls := root.Get("parallel_tool_calls"); parallelToolCalls.Exists() {
		out, _ = sjson.SetBytes(out, "parallel_tool_calls", parallelToolCalls.Bool())
	}
	if temperature := root.Get("temperature"); temperature.Exists() {
		out, _ = sjson.SetBytes(out, "temperature", temperature.Float())
	}
	if topP := root.Get("top_p"); topP.Exists() {
		out, _ = sjson.SetBytes(out, "top_p", topP.Float())
	}
	if presencePenalty := root.Get("presence_penalty"); presencePenalty.Exists() {
		out, _ = sjson.SetBytes(out, "presence_penalty", presencePenalty.Float())
	}
	if frequencyPenalty := root.Get("frequency_penalty"); frequencyPenalty.Exists() {
		out, _ = sjson.SetBytes(out, "frequency_penalty", frequencyPenalty.Float())
	}
	if stop := root.Get("stop"); stop.Exists() {
		out, _ = sjson.SetRawBytes(out, "stop", []byte(stop.Raw))
	}
	if metadata := root.Get("metadata"); metadata.Exists() {
		out, _ = sjson.SetRawBytes(out, "metadata", []byte(metadata.Raw))
	}
	if user := root.Get("user"); user.Exists() && strings.TrimSpace(user.String()) != "" {
		out, _ = sjson.SetBytes(out, "metadata.user_id", user.String())
	}
	if reasoning := root.Get("reasoning"); reasoning.Exists() {
		out, _ = sjson.SetRawBytes(out, "reasoning", []byte(reasoning.Raw))
	}
	if text := root.Get("text"); text.Exists() {
		out, _ = sjson.SetRawBytes(out, "text", []byte(text.Raw))
	}

	var instructions []string
	if messages := root.Get("messages"); messages.Exists() && messages.IsArray() {
		for _, message := range messages.Array() {
			role := strings.TrimSpace(message.Get("role").String())
			if role == "" {
				continue
			}

			if role == "system" || role == "developer" {
				if text := flattenChatMessageText(message.Get("content")); text != "" {
					instructions = append(instructions, text)
				}
				continue
			}

			if role == "tool" {
				callID := strings.TrimSpace(message.Get("tool_call_id").String())
				if callID == "" {
					continue
				}
				item := []byte(`{"type":"function_call_output","call_id":"","output":""}`)
				item, _ = sjson.SetBytes(item, "call_id", callID)
				item, _ = sjson.SetBytes(item, "output", flattenChatMessageText(message.Get("content")))
				out, _ = sjson.SetRawBytes(out, "input.-1", item)
				continue
			}

			if role == "assistant" {
				if assistantMessage := convertChatMessageToResponsesMessage(role, message.Get("content")); assistantMessage != nil {
					out, _ = sjson.SetRawBytes(out, "input.-1", assistantMessage)
				}
				if toolCalls := message.Get("tool_calls"); toolCalls.Exists() && toolCalls.IsArray() {
					for _, toolCall := range toolCalls.Array() {
						item := []byte(`{"type":"function_call","call_id":"","name":"","arguments":""}`)
						if callID := strings.TrimSpace(toolCall.Get("id").String()); callID != "" {
							item, _ = sjson.SetBytes(item, "call_id", callID)
						}
						if name := strings.TrimSpace(toolCall.Get("function.name").String()); name != "" {
							item, _ = sjson.SetBytes(item, "name", name)
						}
						if args := toolCall.Get("function.arguments"); args.Exists() {
							item, _ = sjson.SetBytes(item, "arguments", args.String())
						}
						out, _ = sjson.SetRawBytes(out, "input.-1", item)
					}
				}
				continue
			}

			if userMessage := convertChatMessageToResponsesMessage(role, message.Get("content")); userMessage != nil {
				out, _ = sjson.SetRawBytes(out, "input.-1", userMessage)
			}
		}
	}
	if len(instructions) > 0 {
		out, _ = sjson.SetBytes(out, "instructions", strings.Join(instructions, "\n\n"))
	}

	if tools := root.Get("tools"); tools.Exists() && tools.IsArray() {
		for _, tool := range tools.Array() {
			toolType := strings.TrimSpace(tool.Get("type").String())
			if toolType != "" && toolType != "function" {
				out, _ = sjson.SetRawBytes(out, "tools.-1", []byte(tool.Raw))
				continue
			}

			respTool := []byte(`{"type":"function","name":"","description":"","parameters":{}}`)
			if name := strings.TrimSpace(tool.Get("function.name").String()); name != "" {
				respTool, _ = sjson.SetBytes(respTool, "name", name)
			}
			if description := tool.Get("function.description"); description.Exists() {
				respTool, _ = sjson.SetBytes(respTool, "description", description.String())
			}
			if parameters := tool.Get("function.parameters"); parameters.Exists() {
				respTool, _ = sjson.SetRawBytes(respTool, "parameters", []byte(parameters.Raw))
			}
			out, _ = sjson.SetRawBytes(out, "tools.-1", respTool)
		}
	}

	if toolChoice := root.Get("tool_choice"); toolChoice.Exists() {
		if toolChoice.Type == gjson.String {
			out, _ = sjson.SetBytes(out, "tool_choice", toolChoice.String())
		} else if toolChoice.Get("type").String() == "function" {
			respToolChoice := []byte(`{"type":"function","name":""}`)
			if name := strings.TrimSpace(toolChoice.Get("function.name").String()); name != "" {
				respToolChoice, _ = sjson.SetBytes(respToolChoice, "name", name)
			}
			out, _ = sjson.SetRawBytes(out, "tool_choice", respToolChoice)
		} else {
			out, _ = sjson.SetRawBytes(out, "tool_choice", []byte(toolChoice.Raw))
		}
	}

	if len(gjson.GetBytes(out, "input").Array()) == 0 {
		out, _ = sjson.DeleteBytes(out, "input")
	}

	return out
}

func convertChatMessageToResponsesMessage(role string, content gjson.Result) []byte {
	parts := convertChatContentToResponsesParts(role, content)
	if len(parts) == 0 {
		return nil
	}

	item := []byte(`{"type":"message","role":"","content":[]}`)
	item, _ = sjson.SetBytes(item, "role", role)
	for _, part := range parts {
		item, _ = sjson.SetRawBytes(item, "content.-1", part)
	}
	return item
}

func convertChatContentToResponsesParts(role string, content gjson.Result) [][]byte {
	if !content.Exists() {
		return nil
	}

	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}

	var parts [][]byte
	appendText := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		part := []byte(`{"type":"","text":""}`)
		part, _ = sjson.SetBytes(part, "type", textType)
		part, _ = sjson.SetBytes(part, "text", text)
		parts = append(parts, part)
	}

	if content.Type == gjson.String {
		appendText(content.String())
		return parts
	}
	if !content.IsArray() {
		appendText(content.String())
		return parts
	}

	for _, entry := range content.Array() {
		entryType := strings.TrimSpace(entry.Get("type").String())
		switch entryType {
		case "", "text", "input_text", "output_text":
			text := entry.Get("text").String()
			if text == "" {
				text = entry.Get("content").String()
			}
			appendText(text)
		case "image_url":
			url := strings.TrimSpace(entry.Get("image_url.url").String())
			if url == "" {
				url = strings.TrimSpace(entry.Get("image_url").String())
			}
			if url == "" {
				continue
			}
			part := []byte(`{"type":"input_image","image_url":""}`)
			part, _ = sjson.SetBytes(part, "image_url", url)
			parts = append(parts, part)
		case "input_image":
			url := strings.TrimSpace(entry.Get("image_url").String())
			if url == "" {
				continue
			}
			part := []byte(`{"type":"input_image","image_url":""}`)
			part, _ = sjson.SetBytes(part, "image_url", url)
			parts = append(parts, part)
		}
	}

	return parts
}

func flattenChatMessageText(content gjson.Result) string {
	if !content.Exists() {
		return ""
	}
	if content.Type == gjson.String {
		return content.String()
	}
	if !content.IsArray() {
		return content.String()
	}

	var parts []string
	for _, item := range content.Array() {
		text := item.Get("text").String()
		if text == "" {
			text = item.Get("content").String()
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}
