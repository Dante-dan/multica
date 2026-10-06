package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func discoverCursorModelsForRun(ctx context.Context, command Command, env map[string]string) ([]Model, bool) {
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := command.exec(runCtx, "--list-models")
	cmd.Env = buildEnv(env)
	hideAgentWindow(cmd)
	out, err := outputOwned(cmd, command.logger)
	if err != nil && len(out) == 0 {
		return nil, false
	}
	models := parseCursorModels(string(out))
	return models, len(models) > 0
}

// parseCursorParameterizedModel splits the exact selector emitted by
// cursor-agent --list-models. Parameter values deliberately remain opaque:
// Cursor owns this catalog and is the only authority on supported values.
func parseCursorParameterizedModel(selector string) (string, map[string]string, bool) {
	selector = strings.TrimSpace(selector)
	open := strings.IndexByte(selector, '[')
	if open <= 0 || !strings.HasSuffix(selector, "]") {
		return "", nil, false
	}
	base := selector[:open]
	if !isOpenclawIdentifier(base) {
		return "", nil, false
	}
	body := selector[open+1 : len(selector)-1]
	params := make(map[string]string)
	for _, item := range strings.Split(body, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" || !isCursorParameterToken(key) || !isCursorParameterToken(value) {
			return "", nil, false
		}
		if _, duplicate := params[key]; duplicate {
			return "", nil, false
		}
		params[key] = value
	}
	if len(params) == 0 {
		return "", nil, false
	}
	return base, params, true
}

func isCursorParameterToken(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-._/", r) {
			continue
		}
		return false
	}
	return value != ""
}

func cursorCatalogContains(models []Model, selector string) bool {
	for _, model := range models {
		if model.ID == selector {
			return true
		}
	}
	return false
}

// prepareCursorRunConfig copies the user's CLI config into a private directory
// and overlays only this run's model parameters. The caller removes the
// returned directory after cursor-agent exits.
func prepareCursorRunConfig(env map[string]string, model string, params map[string]string) (string, error) {
	dir, err := os.MkdirTemp("", "multica-cursor-config-")
	if err != nil {
		return "", fmt.Errorf("create private Cursor config: %w", err)
	}
	cleanup := func(err error) (string, error) {
		_ = os.RemoveAll(dir)
		return "", err
	}

	config := map[string]any{}
	sourceDir := strings.TrimSpace(env["CURSOR_CONFIG_DIR"])
	if sourceDir == "" {
		sourceDir = strings.TrimSpace(os.Getenv("CURSOR_CONFIG_DIR"))
	}
	if sourceDir == "" {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			sourceDir = filepath.Join(home, ".cursor")
		}
	}
	if sourceDir != "" {
		raw, readErr := os.ReadFile(filepath.Join(sourceDir, "cli-config.json"))
		switch {
		case readErr == nil:
			if err := json.Unmarshal(raw, &config); err != nil {
				return cleanup(fmt.Errorf("parse Cursor cli-config.json: %w", err))
			}
		case !os.IsNotExist(readErr):
			return cleanup(fmt.Errorf("read Cursor cli-config.json: %w", readErr))
		}
	}

	selected, _ := config["selectedModel"].(map[string]any)
	if selected == nil {
		selected = map[string]any{}
	}
	selected["modelId"] = model
	selected["parameters"] = params
	config["selectedModel"] = selected

	modelParameters, _ := config["modelParameters"].(map[string]any)
	if modelParameters == nil {
		modelParameters = map[string]any{}
	}
	modelParameters[model] = params
	config["modelParameters"] = modelParameters

	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return cleanup(fmt.Errorf("encode private Cursor config: %w", err))
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(dir, "cli-config.json"), raw, 0o600); err != nil {
		return cleanup(fmt.Errorf("write private Cursor config: %w", err))
	}
	return dir, nil
}
