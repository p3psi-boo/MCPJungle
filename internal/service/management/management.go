// Package management provides the tools of the built-in `mcpjungle` MCP server, which let an
// agent inspect and manage the gateway itself: MCP servers, tools and tool groups.
package management

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/internal/service/toolgroup"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"gorm.io/datatypes"
)

const (
	listServersToolName      = "list_servers"
	setServerEnabledToolName = "set_server_enabled"
	listToolsToolName        = "list_tools"
	setToolsEnabledToolName  = "set_tools_enabled"
	listToolGroupsToolName   = "list_tool_groups"
	getToolGroupToolName     = "get_tool_group"
	createToolGroupToolName  = "create_tool_group"
	updateToolGroupToolName  = "update_tool_group"
	deleteToolGroupToolName  = "delete_tool_group"
)

// Service implements the management tools on top of the MCP and tool group services.
type Service struct {
	mcpService       *mcp.MCPService
	toolGroupService *toolgroup.ToolGroupService
}

// NewService creates the management tools service.
func NewService(mcpService *mcp.MCPService, toolGroupService *toolgroup.ToolGroupService) *Service {
	return &Service{mcpService: mcpService, toolGroupService: toolGroupService}
}

// Register exposes the management tools through the MCP proxy servers.
func (s *Service) Register() error {
	return s.mcpService.RegisterManagementTools(s.Tools()...)
}

type serverSummary struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	Transport   types.McpServerTransport `json:"transport"`
	Enabled     bool                     `json:"enabled"`
}

type toolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type toolGroupEndpoints struct {
	StreamableHTTP string `json:"streamable_http"`
	SSE            string `json:"sse"`
}

type toolGroupView struct {
	*types.ToolGroup
	// Endpoints are paths relative to the gateway's base URL.
	Endpoints      toolGroupEndpoints `json:"endpoints"`
	EffectiveTools []string           `json:"effective_tools,omitempty"`
}

// toolGroupArgs holds the arguments of create_tool_group and update_tool_group.
// Pointer fields distinguish "not provided" (keep the current value on update) from "set to empty".
type toolGroupArgs struct {
	Name            string    `json:"name"`
	Description     *string   `json:"description"`
	IncludedTools   *[]string `json:"included_tools"`
	IncludedServers *[]string `json:"included_servers"`
	ExcludedTools   *[]string `json:"excluded_tools"`
}

func toolName(name string) string {
	return mcp.ManagementServerName + "__" + name
}

// Tools returns the management tools, named `mcpjungle__<tool>`.
func (s *Service) Tools() []server.ServerTool {
	groupMembershipOptions := []mcpgo.ToolOption{
		mcpgo.WithArray(
			"included_tools",
			mcpgo.WithStringItems(),
			mcpgo.Description("Canonical names of tools to include, eg- github__create_issue"),
		),
		mcpgo.WithArray(
			"included_servers",
			mcpgo.WithStringItems(),
			mcpgo.Description("Names of MCP servers whose tools are all included, including tools they add later"),
		),
		mcpgo.WithArray(
			"excluded_tools",
			mcpgo.WithStringItems(),
			mcpgo.Description("Canonical names of tools to leave out; exclusions win over inclusions"),
		),
	}

	createOptions := append([]mcpgo.ToolOption{
		mcpgo.WithDescription(
			"Create a tool group: a named subset of tools served on its own MCP endpoint, so that a client " +
				"only sees the tools it needs. The group must resolve to at least one enabled tool. " +
				"Use " + toolName(listToolsToolName) + " to find tool names.",
		),
		mcpgo.WithString(
			"name",
			mcpgo.Required(),
			mcpgo.Description("Unique group name; letters, digits, '_' and '-', starting with a letter or digit"),
		),
		mcpgo.WithString("description", mcpgo.Description("What the group is for")),
		mcpgo.WithDestructiveHintAnnotation(false),
		mcpgo.WithOpenWorldHintAnnotation(false),
	}, groupMembershipOptions...)

	updateOptions := append([]mcpgo.ToolOption{
		mcpgo.WithDescription(
			"Update an existing tool group. Only the fields you pass are changed; each list you pass replaces " +
				"the current list. Clients connected to the group see the change immediately.",
		),
		mcpgo.WithString("name", mcpgo.Required(), mcpgo.Description("Name of the tool group to update")),
		mcpgo.WithString("description", mcpgo.Description("New description")),
		mcpgo.WithDestructiveHintAnnotation(true),
		mcpgo.WithIdempotentHintAnnotation(true),
		mcpgo.WithOpenWorldHintAnnotation(false),
	}, groupMembershipOptions...)

	return []server.ServerTool{
		{
			Tool: mcpgo.NewTool(
				toolName(listServersToolName),
				mcpgo.WithDescription("List the MCP servers registered in the gateway, with their transport and whether they are enabled."),
				readOnly(),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleListServers,
		},
		{
			Tool: mcpgo.NewTool(
				toolName(setServerEnabledToolName),
				mcpgo.WithDescription(
					"Enable or disable an MCP server. Disabling hides all of its tools, prompts and resources "+
						"from every client and tool group until it is enabled again.",
				),
				mcpgo.WithString("name", mcpgo.Required(), mcpgo.Description("Name of the MCP server")),
				mcpgo.WithBoolean("enabled", mcpgo.Required(), mcpgo.Description("true to enable, false to disable")),
				mcpgo.WithDestructiveHintAnnotation(true),
				mcpgo.WithIdempotentHintAnnotation(true),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleSetServerEnabled,
		},
		{
			Tool: mcpgo.NewTool(
				toolName(listToolsToolName),
				mcpgo.WithDescription(
					"List the tools registered in the gateway by their canonical names (<server>__<tool>), "+
						"including disabled ones. These are the names used in tool groups.",
				),
				mcpgo.WithString("server", mcpgo.Description("Only list the tools of this MCP server")),
				readOnly(),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleListTools,
		},
		{
			Tool: mcpgo.NewTool(
				toolName(setToolsEnabledToolName),
				mcpgo.WithDescription(
					"Enable or disable tools. Each name is either a canonical tool name (<server>__<tool>) or "+
						"a server name, which applies to all of that server's tools. Disabled tools are hidden "+
						"from every client and tool group.",
				),
				mcpgo.WithArray(
					"names",
					mcpgo.Required(),
					mcpgo.WithStringItems(),
					mcpgo.MinItems(1),
					mcpgo.Description("Tool or server names"),
				),
				mcpgo.WithBoolean("enabled", mcpgo.Required(), mcpgo.Description("true to enable, false to disable")),
				mcpgo.WithDestructiveHintAnnotation(true),
				mcpgo.WithIdempotentHintAnnotation(true),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleSetToolsEnabled,
		},
		{
			Tool: mcpgo.NewTool(
				toolName(listToolGroupsToolName),
				mcpgo.WithDescription("List the tool groups with their configuration and MCP endpoints."),
				readOnly(),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleListToolGroups,
		},
		{
			Tool: mcpgo.NewTool(
				toolName(getToolGroupToolName),
				mcpgo.WithDescription(
					"Get a tool group's configuration, MCP endpoints and effective tools, ie, the tools it "+
						"actually serves after resolving included servers and exclusions.",
				),
				mcpgo.WithString("name", mcpgo.Required(), mcpgo.Description("Name of the tool group")),
				readOnly(),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleGetToolGroup,
		},
		{
			Tool:    mcpgo.NewTool(toolName(createToolGroupToolName), createOptions...),
			Handler: s.handleCreateToolGroup,
		},
		{
			Tool:    mcpgo.NewTool(toolName(updateToolGroupToolName), updateOptions...),
			Handler: s.handleUpdateToolGroup,
		},
		{
			Tool: mcpgo.NewTool(
				toolName(deleteToolGroupToolName),
				mcpgo.WithDescription(
					"Delete a tool group. Its MCP endpoint stops working; the tools themselves are not affected.",
				),
				mcpgo.WithString("name", mcpgo.Required(), mcpgo.Description("Name of the tool group to delete")),
				mcpgo.WithDestructiveHintAnnotation(true),
				mcpgo.WithIdempotentHintAnnotation(false),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleDeleteToolGroup,
		},
	}
}

func readOnly() mcpgo.ToolOption {
	return func(t *mcpgo.Tool) {
		mcpgo.WithReadOnlyHintAnnotation(true)(t)
		mcpgo.WithDestructiveHintAnnotation(false)(t)
	}
}

func (s *Service) handleListServers(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	servers, err := s.mcpService.ListMcpServers()
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	result := make([]serverSummary, len(servers))
	for i, srv := range servers {
		result[i] = serverSummary{
			Name:        srv.Name,
			Description: srv.Description,
			Transport:   srv.Transport,
			Enabled:     srv.Enabled,
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return mcpgo.NewToolResultJSON(map[string]any{"servers": result})
}

func (s *Service) handleSetServerEnabled(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	enabled, err := request.RequireBool("enabled")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if err := s.mcpService.SetDashboardServerEnabled(name, enabled); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return mcpgo.NewToolResultJSON(map[string]any{"name": name, "enabled": enabled})
}

func (s *Service) handleListTools(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	var (
		tools []model.Tool
		err   error
	)
	if serverName := strings.TrimSpace(request.GetString("server", "")); serverName != "" {
		tools, err = s.mcpService.ListToolsByServer(serverName)
	} else {
		tools, err = s.mcpService.ListTools()
	}
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	result := make([]toolSummary, len(tools))
	for i, t := range tools {
		result[i] = toolSummary{Name: t.Name, Description: t.Description, Enabled: t.Enabled}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return mcpgo.NewToolResultJSON(map[string]any{"tools": result})
}

func (s *Service) handleSetToolsEnabled(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	names, err := request.RequireStringSlice("names")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if len(names) == 0 {
		return mcpgo.NewToolResultError("names must contain at least one tool or server name"), nil
	}
	enabled, err := request.RequireBool("enabled")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}

	changed := []string{}
	for _, name := range names {
		var toolNames []string
		if enabled {
			toolNames, err = s.mcpService.EnableTools(name)
		} else {
			toolNames, err = s.mcpService.DisableTools(name)
		}
		if err != nil {
			return mcpgo.NewToolResultError(fmt.Sprintf("%s (tools updated before the failure: %v)", err, changed)), nil
		}
		changed = append(changed, toolNames...)
	}
	return mcpgo.NewToolResultJSON(map[string]any{"enabled": enabled, "tools": changed})
}

func (s *Service) handleListToolGroups(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	groups, err := s.toolGroupService.ListToolGroups()
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	result := make([]*toolGroupView, len(groups))
	for i := range groups {
		view, err := newToolGroupView(&groups[i])
		if err != nil {
			return mcpgo.NewToolResultError(err.Error()), nil
		}
		result[i] = view
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return mcpgo.NewToolResultJSON(map[string]any{"tool_groups": result})
}

func (s *Service) handleGetToolGroup(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return s.toolGroupResult(name)
}

func (s *Service) handleCreateToolGroup(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	var args toolGroupArgs
	if err := request.BindArguments(&args); err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	group := &model.ToolGroup{Name: args.Name}
	if err := args.applyTo(group); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if err := s.toolGroupService.CreateToolGroup(group); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return s.toolGroupResult(group.Name)
}

func (s *Service) handleUpdateToolGroup(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	var args toolGroupArgs
	if err := request.BindArguments(&args); err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	if args.Name == "" {
		return mcpgo.NewToolResultError("name is required"), nil
	}
	current, err := s.toolGroupService.GetToolGroup(args.Name)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	updated := &model.ToolGroup{
		Name:            current.Name,
		Description:     current.Description,
		IncludedTools:   current.IncludedTools,
		IncludedServers: current.IncludedServers,
		ExcludedTools:   current.ExcludedTools,
	}
	if err := args.applyTo(updated); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if _, err := s.toolGroupService.UpdateToolGroup(args.Name, updated); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return s.toolGroupResult(args.Name)
}

func (s *Service) handleDeleteToolGroup(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	// DeleteToolGroup succeeds for unknown names, so check first to report a typo to the agent.
	if _, err := s.toolGroupService.GetToolGroup(name); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if err := s.toolGroupService.DeleteToolGroup(name); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return mcpgo.NewToolResultJSON(map[string]any{"deleted": name})
}

// toolGroupResult reads the group back from the database so the agent sees what was actually stored.
func (s *Service) toolGroupResult(name string) (*mcpgo.CallToolResult, error) {
	group, err := s.toolGroupService.GetToolGroup(name)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	view, err := newToolGroupView(group)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	view.EffectiveTools, err = s.toolGroupService.ResolveEffectiveTools(name)
	if err != nil {
		return mcpgo.NewToolResultError(fmt.Sprintf("tool group %s exists but its tools could not be resolved: %v", name, err)), nil
	}
	return mcpgo.NewToolResultJSON(view)
}

func (a *toolGroupArgs) applyTo(group *model.ToolGroup) error {
	if a.Description != nil {
		group.Description = *a.Description
	}
	for _, field := range []struct {
		value  *[]string
		target *datatypes.JSON
	}{
		{a.IncludedTools, &group.IncludedTools},
		{a.IncludedServers, &group.IncludedServers},
		{a.ExcludedTools, &group.ExcludedTools},
	} {
		if field.value == nil {
			continue
		}
		list := *field.value
		if list == nil {
			list = []string{}
		}
		raw, err := json.Marshal(list)
		if err != nil {
			return err
		}
		*field.target = raw
	}
	return nil
}

func newToolGroupView(g *model.ToolGroup) (*toolGroupView, error) {
	tools, err := g.GetTools()
	if err != nil {
		return nil, fmt.Errorf("failed to read included tools of group %s: %w", g.Name, err)
	}
	servers, err := g.GetServers()
	if err != nil {
		return nil, fmt.Errorf("failed to read included servers of group %s: %w", g.Name, err)
	}
	excluded, err := g.GetExcludedTools()
	if err != nil {
		return nil, fmt.Errorf("failed to read excluded tools of group %s: %w", g.Name, err)
	}
	base := "/v0/groups/" + g.Name
	return &toolGroupView{
		ToolGroup: &types.ToolGroup{
			Name:            g.Name,
			Description:     g.Description,
			IncludedTools:   tools,
			IncludedServers: servers,
			ExcludedTools:   excluded,
		},
		Endpoints: toolGroupEndpoints{StreamableHTTP: base + "/mcp", SSE: base + "/sse"},
	}, nil
}
