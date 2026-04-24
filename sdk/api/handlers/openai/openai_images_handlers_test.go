package openai

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildImagesAPIResponseSavesGeneratedImageAndMetadata(t *testing.T) {
	tempDir := t.TempDir()
	oldDir := imagesSaveDir
	imagesSaveDir = tempDir
	t.Cleanup(func() {
		imagesSaveDir = oldDir
	})

	raw := []byte("test-image-bytes")
	out, err := buildImagesAPIResponse([]imageCallResult{
		{
			Result:        base64.StdEncoding.EncodeToString(raw),
			RevisedPrompt: "draw a cat",
			OutputFormat:  "png",
			CallID:        "call_123",
		},
	}, 123, []byte(`{"total_tokens":42}`), imageCallResult{}, "b64_json")
	if err != nil {
		t.Fatalf("buildImagesAPIResponse returned error: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected response payload")
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 saved files, got %d", len(entries))
	}

	var imagePath string
	var infoPath string
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".png"):
			imagePath = filepath.Join(tempDir, name)
		case strings.HasSuffix(name, ".info.json"):
			infoPath = filepath.Join(tempDir, name)
		}
	}
	if imagePath == "" {
		t.Fatal("expected saved image file")
	}
	if infoPath == "" {
		t.Fatal("expected saved metadata file")
	}

	savedImage, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatalf("read saved image failed: %v", err)
	}
	if string(savedImage) != string(raw) {
		t.Fatalf("saved image mismatch: got %q want %q", string(savedImage), string(raw))
	}

	infoBytes, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("read metadata failed: %v", err)
	}
	var info map[string]interface{}
	if err := json.Unmarshal(infoBytes, &info); err != nil {
		t.Fatalf("unmarshal metadata failed: %v", err)
	}
	if info["revised_prompt"] != "draw a cat" {
		t.Fatalf("unexpected revised_prompt: %v", info["revised_prompt"])
	}
	if info["call_id"] != "call_123" {
		t.Fatalf("unexpected call_id: %v", info["call_id"])
	}
	if info["created"] != float64(123) {
		t.Fatalf("unexpected created: %v", info["created"])
	}
	usage, ok := info["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usage object, got %T", info["usage"])
	}
	if usage["total_tokens"] != float64(42) {
		t.Fatalf("unexpected usage total_tokens: %v", usage["total_tokens"])
	}
}
