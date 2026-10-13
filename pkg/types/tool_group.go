package types

// ToolGroup represents a group (collection) of MCP tools, prompts, resources and skills.
// A group can contain a subset of all available items in the MCPJungle system.
// This allows you to expose a limited set of capabilities to certain mcp clients.
// This struct is also the basis for the JSON configuration file used to register a new group.
type ToolGroup struct {
	// Name is the unique name of the group (mandatory).
	Name string `json:"name"`
	// IncludedTools is a list of specific tools included in this group.
	IncludedTools []string `json:"included_tools,omitempty"`
	// IncludedServers is a list of MCP server names. All tools from these servers will be included.
	IncludedServers []string `json:"included_servers,omitempty"`
	// ExcludedTools is a list of tools to exclude from the group (useful with IncludedServers).
	ExcludedTools []string `json:"excluded_tools,omitempty"`
	// IncludedPrompts is a list of specific prompt names included in this group.
	IncludedPrompts []string `json:"included_prompts,omitempty"`
	// IncludedResources is a list of specific resource URIs included in this group.
	IncludedResources []string `json:"included_resources,omitempty"`
	// IncludedSkills is a list of specific Agent Skill names included in this group.
	IncludedSkills []string `json:"included_skills,omitempty"`

	Description string `json:"description"`
}

// ToolGroupEndpoints contains the endpoints a MCP client can use to access a tool group.
type ToolGroupEndpoints struct {
	StreamableHTTPEndpoint string `json:"streamable_http_endpoint"`
	SSEEndpoint            string `json:"sse_endpoint"`
	SSEMessageEndpoint     string `json:"sse_message_endpoint"`
}

type CreateToolGroupResponse struct {
	*ToolGroupEndpoints
}

type GetToolGroupResponse struct {
	*ToolGroup
	*ToolGroupEndpoints
}

// UpdateToolGroupResponse contains the old and new configuration of a tool group after a successful update.
type UpdateToolGroupResponse struct {
	Name string `json:"name"`

	// Old contains the original configuration of the tool group.
	Old *ToolGroup `json:"old"`
	// New contains the now-live configuration of the tool group.
	New *ToolGroup `json:"new"`
}
