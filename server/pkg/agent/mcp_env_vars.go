package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
)

var mcpEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var codebuddyMcpEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// AdaptMcpEnvReferences resolves Multica's names-only intent into native
// configuration before task-local MCP materialization. No secret values are
// serialized: providers expand the references from the task process environment.
func AdaptMcpEnvReferences(provider string, raw json.RawMessage, env map[string]string) (json.RawMessage, error) {
	if !hasManagedMcpConfig(raw) {
		return raw, nil
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse MCP configuration: %w", err)
	}
	serversRaw, exists := document["mcpServers"]
	if !exists {
		return raw, nil
	}
	var servers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(serversRaw, &servers); err != nil {
		return nil, fmt.Errorf("parse MCP servers: %w", err)
	}
	changed := false
	for name, server := range servers {
		refsRaw, present := server["env_vars"]
		if !present {
			continue
		}
		if provider != "claude" && provider != "codex" && provider != "cursor" && provider != "codebuddy" {
			return nil, fmt.Errorf("MCP server %q: env_vars is unsupported for runtime %q", name, provider)
		}
		if _, command := server["command"]; !command {
			return nil, fmt.Errorf("MCP server %q: env_vars requires STDIO", name)
		}
		var refs []string
		if err := json.Unmarshal(refsRaw, &refs); err != nil {
			return nil, fmt.Errorf("MCP server %q: env_vars must be an array of variable names", name)
		}
		literal := map[string]string{}
		if value, ok := server["env"]; ok {
			if err := json.Unmarshal(value, &literal); err != nil {
				return nil, fmt.Errorf("MCP server %q: env must contain string values", name)
			}
			if literal == nil {
				literal = map[string]string{}
			}
		}
		seen := map[string]bool{}
		for _, key := range refs {
			if !mcpEnvName.MatchString(key) || (provider == "codebuddy" && !codebuddyMcpEnvName.MatchString(key)) {
				return nil, fmt.Errorf("MCP server %q: invalid environment variable name", name)
			}
			if seen[key] {
				return nil, fmt.Errorf("MCP server %q: repeated environment variable %q", name, key)
			}
			seen[key] = true
			if _, conflict := literal[key]; conflict {
				return nil, fmt.Errorf("MCP server %q: environment variable %q appears in both env and env_vars", name, key)
			}
			if _, configured := env[key]; !configured {
				return nil, fmt.Errorf("MCP server %q: environment variable %q is not configured on the Agent", name, key)
			}
			if provider == "cursor" {
				literal[key] = "${env:" + key + "}"
			} else if provider != "codex" {
				literal[key] = "${" + key + "}"
			}
		}
		if provider != "codex" {
			delete(server, "env_vars")
			if len(literal) > 0 {
				server["env"], _ = json.Marshal(literal)
			}
		}
		changed = true
	}
	if !changed {
		return raw, nil
	}
	document["mcpServers"], _ = json.Marshal(servers)
	return json.Marshal(document)
}
