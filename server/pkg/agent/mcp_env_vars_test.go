package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdaptMcpEnvReferences(t *testing.T) {
	raw := json.RawMessage(`{"mcpServers":{"tapd":{"command":"uvx","env_vars":["TAPD_ACCESS_TOKEN"],"env":{"BASE_URL":"https://example.com"}}}}`)
	for provider, want := range map[string]string{"codex": `"env_vars":["TAPD_ACCESS_TOKEN"]`, "claude": "${TAPD_ACCESS_TOKEN}", "codebuddy": "${TAPD_ACCESS_TOKEN}", "cursor": "${env:TAPD_ACCESS_TOKEN}"} {
		t.Run(provider, func(t *testing.T) {
			got, err := AdaptMcpEnvReferences(provider, raw, map[string]string{"TAPD_ACCESS_TOKEN": "private-value"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), want) || strings.Contains(string(got), "private-value") {
				t.Fatalf("incorrect names-only adaptation: %s", got)
			}
			if !strings.Contains(string(got), "BASE_URL") {
				t.Fatal("literal environment was lost")
			}
		})
	}
	for _, tc := range []struct{ name, provider, input string }{
		{"missing", "claude", string(raw)},
		{"unsupported", "grok", string(raw)},
		{"conflict", "claude", `{"mcpServers":{"tapd":{"command":"uvx","env_vars":["TOKEN"],"env":{"TOKEN":"private-value"}}}}`},
		{"http", "codex", `{"mcpServers":{"tapd":{"url":"https://example.com","env_vars":["TOKEN"]}}}`},
		{"wrong shape", "cursor", `{"mcpServers":{"tapd":{"command":"uvx","env_vars":"private-value"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AdaptMcpEnvReferences(tc.provider, json.RawMessage(tc.input), nil)
			if err == nil {
				t.Fatal("expected configuration error")
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatal("error leaked a value")
			}
		})
	}
}
