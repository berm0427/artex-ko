package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/mcp"
)

// Run with REA_MCP_NODE and REA_MCP_ENTRY set to an installed REA's Node
// executable and rea.mjs entrypoint. Kept opt-in because REA is not a project
// dependency and a cold start may take several minutes on Windows.
func TestREAStdioDiscoveryIntegration(t *testing.T) {
	node, entry, launcher := os.Getenv("REA_MCP_NODE"), os.Getenv("REA_MCP_ENTRY"), os.Getenv("REA_MCP_LAUNCHER")
	if node == "" || entry == "" || launcher == "" {
		t.Skip("REA_MCP_NODE, REA_MCP_ENTRY and REA_MCP_LAUNCHER are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	args := []string{launcher, entry}
	if distro, ghidraDir := os.Getenv("REA_MCP_WSL_DISTRO"), os.Getenv("REA_MCP_WSL_GHIDRA_DIR"); distro != "" && ghidraDir != "" {
		args = []string{launcher, distro, entry, ghidraDir}
	}
	client, err := mcp.NewStdioClient(ctx, "rea", node, nil, args...)
	if err != nil {
		t.Fatalf("REA MCP initialize: %v", err)
	}
	defer client.Close()
	tools, err := client.Tools(ctx)
	if err != nil {
		t.Fatalf("REA MCP tools/list: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("REA returned no MCP tools")
	}
	t.Logf("discovered %d REA MCP tools", len(tools))
	if binary := os.Getenv("REA_MCP_TEST_BINARY"); binary != "" {
		call := func(name string, args map[string]any) {
			t.Helper()
			for _, candidate := range tools {
				if candidate.Name() != "mcp__rea__"+name {
					continue
				}
				input, _ := json.Marshal(args)
				result, err := candidate.Call(ctx, input, nil)
				if err != nil || result.IsError || strings.Contains(result.Flatten(), "\"error\"") {
					t.Fatalf("REA %s failed: %v %s", name, err, result.Flatten())
				}
				t.Logf("REA %s succeeded", name)
				return
			}
			t.Fatalf("REA tool %s not discovered", name)
		}
		call("open_binary", map[string]any{"path": binary})
		call("binary_overview", map[string]any{})
	}
}
