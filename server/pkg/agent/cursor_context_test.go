package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseCursorParameterizedModel(t *testing.T) {
	base, params, ok := parseCursorParameterizedModel("grok-4.7[reasoning_effort=high,context=500k,fast=false]")
	if !ok || base != "grok-4.7" {
		t.Fatalf("parsed (%q, %v, %v)", base, params, ok)
	}
	if params["context"] != "500k" || params["reasoning_effort"] != "high" || params["fast"] != "false" {
		t.Fatalf("params = %#v", params)
	}
	for _, invalid := range []string{"grok-4.7", "grok-4.7[]", "grok-4.7[context=500k,context=256k]", "grok-4.7[context=500 k]"} {
		if _, _, ok := parseCursorParameterizedModel(invalid); ok {
			t.Errorf("accepted invalid selector %q", invalid)
		}
	}
}

func TestPrepareCursorRunConfigDoesNotMutateSource(t *testing.T) {
	source := t.TempDir()
	sourcePath := filepath.Join(source, "cli-config.json")
	original := []byte(`{"theme":"dark","selectedModel":{"modelId":"grok-4.7","parameters":{"context":"256k"}}}`)
	if err := os.WriteFile(sourcePath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	dir, err := prepareCursorRunConfig(map[string]string{"CURSOR_CONFIG_DIR": source}, "grok-4.7", map[string]string{"context": "500k"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	gotSource, err := os.ReadFile(sourcePath)
	if err != nil || string(gotSource) != string(original) {
		t.Fatalf("source config changed: %q, err=%v", gotSource, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cli-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	selected := config["selectedModel"].(map[string]any)
	if selected["modelId"] != "grok-4.7" || selected["parameters"].(map[string]any)["context"] != "500k" {
		t.Fatalf("selectedModel = %#v", selected)
	}
	if config["theme"] != "dark" {
		t.Fatalf("unrelated config was not preserved: %#v", config)
	}
}
