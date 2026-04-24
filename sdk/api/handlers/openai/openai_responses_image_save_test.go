package openai

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
)

func TestSaveImagesFromResponsesPayloadSavesGeneratedImage(t *testing.T) {
	tempDir := t.TempDir()
	oldDir := imagesSaveDir
	imagesSaveDir = tempDir
	t.Cleanup(func() {
		imagesSaveDir = oldDir
	})

	saveImagesFromResponsesPayload([]byte(`{
		"id":"resp_123",
		"object":"response",
		"created_at":1777005315,
		"usage":{"input_tokens":151,"output_tokens":7024,"total_tokens":7175},
		"output":[
			{"type":"message","id":"msg_1"},
			{"type":"image_generation_call","id":"ig_1","output_format":"png","revised_prompt":"cat","result":"` + base64.StdEncoding.EncodeToString([]byte("img")) + `"}
		]
	}`))

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 saved files, got %d", len(entries))
	}

	var foundPNG bool
	var foundInfo bool
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".png"):
			foundPNG = true
			raw, err := os.ReadFile(filepath.Join(tempDir, name))
			if err != nil {
				t.Fatalf("read image: %v", err)
			}
			if string(raw) != "img" {
				t.Fatalf("unexpected image content: %q", string(raw))
			}
		case strings.HasSuffix(name, ".info.json"):
			foundInfo = true
			infoBytes, err := os.ReadFile(filepath.Join(tempDir, name))
			if err != nil {
				t.Fatalf("read info: %v", err)
			}
			var info map[string]interface{}
			if err := json.Unmarshal(infoBytes, &info); err != nil {
				t.Fatalf("unmarshal info: %v", err)
			}
			if info["created"] != float64(1777005315) {
				t.Fatalf("unexpected created: %v", info["created"])
			}
			usage, ok := info["usage"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected usage object, got %T", info["usage"])
			}
			if usage["total_tokens"] != float64(7175) {
				t.Fatalf("unexpected usage total_tokens: %v", usage["total_tokens"])
			}
		}
	}
	if !foundPNG {
		t.Fatal("expected saved png")
	}
	if !foundInfo {
		t.Fatal("expected saved info json")
	}
}

func TestForwardResponsesStreamSavesGeneratedImagesOnCompleted(t *testing.T) {
	tempDir := t.TempDir()
	oldDir := imagesSaveDir
	imagesSaveDir = tempDir
	t.Cleanup(func() {
		imagesSaveDir = oldDir
	})

	h, _, c, flusher := newResponsesStreamTestHandler(t)
	data := make(chan []byte, 1)
	errs := make(chan *interfaces.ErrorMessage)
	data <- []byte(`data: {"type":"response.completed","response":{"id":"resp-1","output":[{"type":"image_generation_call","id":"ig_1","output_format":"png","result":"` + base64.StdEncoding.EncodeToString([]byte("stream-img")) + `"}]}}`)
	close(data)
	close(errs)

	h.forwardResponsesStream(c, flusher, func(error) {}, data, errs, nil)

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 saved files, got %d", len(entries))
	}
}
