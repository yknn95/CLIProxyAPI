package openai

import (
	"bytes"
	"encoding/json"

	"github.com/tidwall/gjson"
)

func extractImagesFromResponsesPayload(payload []byte) ([]imageCallResult, imageSaveMetadata) {
	metadata := imageSaveMetadata{}
	if createdAt := gjson.GetBytes(payload, "response.created_at").Int(); createdAt > 0 {
		metadata.CreatedAt = createdAt
	} else if createdAt := gjson.GetBytes(payload, "created_at").Int(); createdAt > 0 {
		metadata.CreatedAt = createdAt
	} else if created := gjson.GetBytes(payload, "created").Int(); created > 0 {
		metadata.CreatedAt = created
	}
	for _, path := range []string{"response.tool_usage.image_gen", "response.usage", "usage"} {
		usage := gjson.GetBytes(payload, path)
		if usage.Exists() && usage.IsObject() {
			metadata.UsageRaw = []byte(usage.Raw)
			break
		}
	}

	output := gjson.GetBytes(payload, "response.output")
	if !output.Exists() || !output.IsArray() {
		output = gjson.GetBytes(payload, "output")
	}
	if !output.Exists() || !output.IsArray() {
		return nil, metadata
	}

	results := make([]imageCallResult, 0, len(output.Array()))
	for _, item := range output.Array() {
		if item.Get("type").String() != "image_generation_call" {
			continue
		}
		res := bytes.TrimSpace([]byte(item.Get("result").String()))
		if len(res) == 0 {
			continue
		}
		results = append(results, imageCallResult{
			Result:        string(res),
			RevisedPrompt: item.Get("revised_prompt").String(),
			OutputFormat:  item.Get("output_format").String(),
			Size:          item.Get("size").String(),
			Background:    item.Get("background").String(),
			Quality:       item.Get("quality").String(),
			CallID:        item.Get("id").String(),
		})
	}
	return results, metadata
}

func saveImagesFromResponsesPayload(payload []byte) {
	results, metadata := extractImagesFromResponsesPayload(payload)
	for idx, img := range results {
		saveGeneratedImage(img, idx, metadata)
	}
}

func saveImagesFromResponsesSSEFrame(frame []byte) {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSpace(bytes.TrimRight(line, "\r"))
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || !json.Valid(payload) {
			continue
		}
		saveImagesFromResponsesPayload(payload)
	}
}
