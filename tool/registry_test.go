package tool

import (
	"fmt"
	"testing"
)

func TestCopyExcludingToolsCopiesCurrentToolsAndExcludesSelectedTools(t *testing.T) {
	sourceToolRegistry := NewRegistry()
	registerTestFunctionTool(t, sourceToolRegistry, "bash", "bash result")
	registerTestFunctionTool(t, sourceToolRegistry, "mcp_playwright__read_page", "page result")
	registerTestFunctionTool(t, sourceToolRegistry, "run_subagent", "subagent result")

	copiedToolRegistry := sourceToolRegistry.CopyExcludingTools("run_subagent")

	if copiedToolRegistry == sourceToolRegistry {
		t.Fatal("CopyExcludingTools returned the source Registry")
	}

	if _, executeExcludedToolError := copiedToolRegistry.Execute(
		"run_subagent",
		map[string]any{},
	); executeExcludedToolError == nil {
		t.Fatal("copied Registry still contains run_subagent")
	}

	bashResult, executeBashError := copiedToolRegistry.Execute("bash", map[string]any{})
	if executeBashError != nil {
		t.Fatalf("execute copied bash tool: %v", executeBashError)
	}
	if bashResult != "bash result" {
		t.Fatalf("bash result = %q, want %q", bashResult, "bash result")
	}

	mcpResult, executeMCPToolError := copiedToolRegistry.Execute(
		"mcp_playwright__read_page",
		map[string]any{},
	)
	if executeMCPToolError != nil {
		t.Fatalf("execute copied MCP tool: %v", executeMCPToolError)
	}
	if mcpResult != "page result" {
		t.Fatalf("MCP result = %q, want %q", mcpResult, "page result")
	}
}

func TestCopyExcludingToolsDoesNotFollowLaterSourceChanges(t *testing.T) {
	sourceToolRegistry := NewRegistry()
	registerTestFunctionTool(t, sourceToolRegistry, "existing_tool", "existing result")

	copiedToolRegistry := sourceToolRegistry.CopyExcludingTools()

	sourceToolRegistry.Unregister("existing_tool")
	registerTestFunctionTool(t, sourceToolRegistry, "later_tool", "later result")

	existingResult, executeExistingToolError := copiedToolRegistry.Execute(
		"existing_tool",
		map[string]any{},
	)
	if executeExistingToolError != nil {
		t.Fatalf("copied Registry lost existing tool: %v", executeExistingToolError)
	}
	if existingResult != "existing result" {
		t.Fatalf("existing result = %q, want %q", existingResult, "existing result")
	}

	if _, executeLaterToolError := copiedToolRegistry.Execute(
		"later_tool",
		map[string]any{},
	); executeLaterToolError == nil {
		t.Fatal("copied Registry contains a tool registered after copying")
	}
}

func registerTestFunctionTool(
	t *testing.T,
	toolRegistry *Registry,
	toolName string,
	toolResult string,
) {
	t.Helper()

	registerToolError := toolRegistry.RegisterFunctionTool(
		toolName,
		fmt.Sprintf("%s description", toolName),
		map[string]any{"type": "object"},
		func(toolArguments map[string]any) (string, error) {
			return toolResult, nil
		},
	)
	if registerToolError != nil {
		t.Fatalf("register %s: %v", toolName, registerToolError)
	}
}
