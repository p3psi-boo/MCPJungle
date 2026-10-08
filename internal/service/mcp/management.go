package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/telemetry"
	"gorm.io/gorm"
)

// ManagementServerName is the name of the built-in virtual MCP server whose tools let an agent
// manage mcpjungle itself (servers, tools and tool groups).
// Its tools are named `mcpjungle__<tool>`.
const ManagementServerName = "mcpjungle"

// managementToolsEnabled is process-wide because ProxyToolFilter is a plain function shared by
// every proxy server, including the ones created for tool groups.
var managementToolsEnabled atomic.Bool

// clientHasServerAccess reports whether an enterprise-mode client may see and call the tools of serverName.
// The management server grants admin-level control, so the wildcard is not enough to reach it:
// the client's allow list must name it explicitly.
func clientHasServerAccess(c *model.McpClient, serverName string) bool {
	if serverName == ManagementServerName && managementToolsEnabled.Load() {
		return c.CheckHasExplicitServerAccess(serverName)
	}
	return c.CheckHasServerAccess(serverName)
}

// RegisterManagementTools exposes the given tools on the MCP proxy servers as the built-in
// `mcpjungle` server. Every tool name must carry the `mcpjungle__` prefix.
// Each handler is wrapped with access control and tool call metrics.
// It must be called at most once, after the proxy servers have been initialized.
func (m *MCPService) RegisterManagementTools(tools ...server.ServerTool) error {
	var existing model.McpServer
	err := m.db.Where("name = ?", ManagementServerName).First(&existing).Error
	if err == nil {
		return fmt.Errorf(
			"an MCP server named %q is already registered, which conflicts with the built-in management server;"+
				" deregister it or disable management tools",
			ManagementServerName,
		)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to check for MCP server %q: %w", ManagementServerName, err)
	}

	wrapped := make([]server.ServerTool, len(tools))
	for i, t := range tools {
		serverName, toolName, ok := splitServerToolName(t.Tool.Name)
		if !ok || serverName != ManagementServerName {
			return fmt.Errorf("management tool %q must be prefixed with %s%s", t.Tool.Name, ManagementServerName, serverToolNameSep)
		}
		wrapped[i] = server.ServerTool{Tool: t.Tool, Handler: m.withManagementToolGuard(toolName, t.Handler)}
	}

	m.managementToolsEnabled = true
	managementToolsEnabled.Store(true)
	m.mcpProxyServer.AddTools(wrapped...)
	m.sseMcpProxyServer.AddTools(wrapped...)
	return nil
}

func (m *MCPService) withManagementToolGuard(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := authorizeProxyServerAccess(ctx, ManagementServerName); err != nil {
			return nil, err
		}
		started := time.Now()
		res, err := handler(ctx, request)
		outcome := telemetry.ToolCallOutcomeSuccess
		if err != nil || (res != nil && res.IsError) {
			outcome = telemetry.ToolCallOutcomeError
		}
		m.metrics.RecordToolCall(ctx, ManagementServerName, toolName, outcome, time.Since(started))
		return res, err
	}
}

// isReservedServerName reports whether name cannot be used for a user-registered MCP server.
func (m *MCPService) isReservedServerName(name string) bool {
	return m.managementToolsEnabled && name == ManagementServerName
}
