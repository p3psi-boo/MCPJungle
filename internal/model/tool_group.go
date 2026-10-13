package model

import (
	"encoding/json"
	"fmt"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ToolResolver defines the interface needed to resolve tools by server.
type ToolResolver interface {
	// ListToolsByServer returns a list of tools for the given MCP server name.
	ListToolsByServer(serverName string) ([]Tool, error)
}

// ToolGroup represents a group of MCP tools, prompts, resources and skills.
// It is useful when the user wants to expose only a subset of capabilities to MCP clients.
type ToolGroup struct {
	gorm.Model

	Name        string `json:"name" gorm:"unique; not null"`
	Description string `json:"description"`

	// IncludedTools contains a list of tool names that are included in this group.
	// storing the list of tool names as a JSON array is a convenient way for now.
	IncludedTools datatypes.JSON `json:"included_tools" gorm:"type:jsonb"`

	// IncludedServers contains a list of MCP server names. All tools from these servers will be included.
	IncludedServers datatypes.JSON `json:"included_servers" gorm:"type:jsonb"`

	// ExcludedTools contains a list of tool names to exclude from the group.
	ExcludedTools datatypes.JSON `json:"excluded_tools" gorm:"type:jsonb"`

	// IncludedPrompts contains a list of canonical prompt names included in this group.
	IncludedPrompts datatypes.JSON `json:"included_prompts" gorm:"type:jsonb"`

	// IncludedResources contains a list of resource URIs included in this group.
	IncludedResources datatypes.JSON `json:"included_resources" gorm:"type:jsonb"`

	// IncludedSkills contains a list of Agent Skill names included in this group.
	IncludedSkills datatypes.JSON `json:"included_skills" gorm:"type:jsonb"`
}

func unmarshalJSONStringList(raw datatypes.JSON) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var items []string
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	if items == nil {
		return []string{}, nil
	}
	return items, nil
}

// GetTools unmarshals the IncludedTools JSON array into a slice of strings.
func (g *ToolGroup) GetTools() ([]string, error) {
	return unmarshalJSONStringList(g.IncludedTools)
}

// GetServers unmarshals the IncludedServers JSON array into a slice of strings.
func (g *ToolGroup) GetServers() ([]string, error) {
	return unmarshalJSONStringList(g.IncludedServers)
}

// GetExcludedTools unmarshals the ExcludedTools JSON array into a slice of strings.
func (g *ToolGroup) GetExcludedTools() ([]string, error) {
	return unmarshalJSONStringList(g.ExcludedTools)
}

// GetPrompts unmarshals the IncludedPrompts JSON array into a slice of strings.
func (g *ToolGroup) GetPrompts() ([]string, error) {
	return unmarshalJSONStringList(g.IncludedPrompts)
}

// GetResources unmarshals the IncludedResources JSON array into a slice of strings.
func (g *ToolGroup) GetResources() ([]string, error) {
	return unmarshalJSONStringList(g.IncludedResources)
}

// GetSkills unmarshals the IncludedSkills JSON array into a slice of strings.
func (g *ToolGroup) GetSkills() ([]string, error) {
	return unmarshalJSONStringList(g.IncludedSkills)
}

// ResolveEffectiveTools resolves all effective tools for this group by combining
// included_tools, included_servers, and applying excluded_tools.
// Note that tool exclusions are applied at last, so if a tool is both included and excluded,
// it will be excluded.
// It requires an MCP service to lookup tools by server.
func (g *ToolGroup) ResolveEffectiveTools(mcpService ToolResolver) ([]string, error) {
	effectiveTools := make(map[string]bool)

	// Add tools from included_tools
	includedTools, err := g.GetTools()
	if err != nil {
		return nil, fmt.Errorf("failed to get included tools: %w", err)
	}
	for _, tool := range includedTools {
		effectiveTools[tool] = true
	}

	// Add tools from included_servers
	includedServers, err := g.GetServers()
	if err != nil {
		return nil, fmt.Errorf("failed to get included servers: %w", err)
	}
	for _, serverName := range includedServers {
		serverTools, err := mcpService.ListToolsByServer(serverName)
		if err != nil {
			return nil, fmt.Errorf("failed to get tools for server %s: %w", serverName, err)
		}
		for _, tool := range serverTools {
			effectiveTools[tool.Name] = true
		}
	}

	// Remove tools from excluded_tools
	excludedTools, err := g.GetExcludedTools()
	if err != nil {
		return nil, fmt.Errorf("failed to get excluded tools: %w", err)
	}
	for _, tool := range excludedTools {
		delete(effectiveTools, tool)
	}

	// Convert map to slice
	result := make([]string, 0, len(effectiveTools))
	for tool := range effectiveTools {
		result = append(result, tool)
	}

	return result, nil
}
