package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/pkg/types"
)

type dashboardToolGroupCreateRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
	Prompts     []string `json:"prompts"`
	Resources   []string `json:"resources"`
	Skills      []string `json:"skills"`
}

type dashboardToolGroupItem struct {
	Name          string `json:"name"`
	CanonicalName string `json:"canonical_name"`
	Server        string `json:"server"`
	Description   string `json:"description,omitempty"`
}

type dashboardToolGroup struct {
	Name                   string                    `json:"name"`
	Description            string                    `json:"description,omitempty"`
	ToolCount              int                       `json:"tool_count"`
	PromptCount            int                       `json:"prompt_count"`
	ResourceCount          int                       `json:"resource_count"`
	SkillCount             int                       `json:"skill_count"`
	Tools                  []dashboardToolGroupItem  `json:"tools"`
	Prompts                []dashboardToolGroupItem  `json:"prompts"`
	Resources              []dashboardToolGroupItem  `json:"resources"`
	Skills                 []dashboardToolGroupItem  `json:"skills"`
	StreamableHTTPEndpoint string                    `json:"streamable_http_endpoint"`
	SSEEndpoint            string                    `json:"sse_endpoint"`
	SSEMessageEndpoint     string                    `json:"sse_message_endpoint"`
}

type dashboardToolGroupsResponse struct {
	ToolGroups []dashboardToolGroup       `json:"tool_groups"`
	EmptyState *types.DashboardEmptyState `json:"empty_state,omitempty"`
}

func (s *Server) dashboardToolGroupsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		groups, err := s.toolGroupService.ListToolGroups()
		if err != nil {
			handleServiceError(c, err)
			return
		}

		resp := dashboardToolGroupsResponse{
			ToolGroups: make([]dashboardToolGroup, 0, len(groups)),
		}
		for _, group := range groups {
			item, err := s.buildDashboardToolGroup(c, group)
			if err != nil {
				handleServiceError(c, err)
				return
			}
			resp.ToolGroups = append(resp.ToolGroups, item)
		}

		if len(resp.ToolGroups) == 0 {
			resp.EmptyState = &types.DashboardEmptyState{
				Title:       "No groups configured yet.",
				Description: "Click + Add Group to expose a focused subset of tools, prompts, resources, and skills on a dedicated endpoint.",
			}
		}

		c.JSON(http.StatusOK, resp)
	}
}

func (s *Server) dashboardGetToolGroupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		group, err := s.toolGroupService.GetToolGroup(c.Param("name"))
		if err != nil {
			handleServiceError(c, err)
			return
		}

		resp, err := s.buildDashboardToolGroup(c, *group)
		if err != nil {
			handleServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

func (s *Server) dashboardCreateToolGroupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var input dashboardToolGroupCreateRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		includedTools, err := json.Marshal(input.Tools)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tools payload"})
			return
		}
		includedPrompts, err := json.Marshal(input.Prompts)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid prompts payload"})
			return
		}
		includedResources, err := json.Marshal(input.Resources)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid resources payload"})
			return
		}
		includedSkills, err := json.Marshal(input.Skills)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid skills payload"})
			return
		}

		group := &model.ToolGroup{
			Name:              input.Name,
			Description:       input.Description,
			IncludedTools:     includedTools,
			IncludedPrompts:   includedPrompts,
			IncludedResources: includedResources,
			IncludedSkills:    includedSkills,
		}
		if err := s.toolGroupService.CreateToolGroup(group); err != nil {
			handleServiceError(c, err)
			return
		}

		resp, err := s.buildDashboardToolGroup(c, *group)
		if err != nil {
			handleServiceError(c, err)
			return
		}
		c.JSON(http.StatusCreated, resp)
	}
}

func (s *Server) dashboardDeleteToolGroupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := s.toolGroupService.DeleteToolGroup(c.Param("name")); err != nil {
			handleServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	}
}

func (s *Server) buildDashboardToolGroup(c *gin.Context, group model.ToolGroup) (dashboardToolGroup, error) {
	toolNames, err := group.ResolveEffectiveTools(s.mcpService)
	if err != nil {
		return dashboardToolGroup{}, err
	}
	promptNames, err := group.GetPrompts()
	if err != nil {
		return dashboardToolGroup{}, err
	}
	resourceURIs, err := group.GetResources()
	if err != nil {
		return dashboardToolGroup{}, err
	}
	skillNames, err := group.GetSkills()
	if err != nil {
		return dashboardToolGroup{}, err
	}

	tools := make([]dashboardToolGroupItem, 0, len(toolNames))
	for _, toolName := range toolNames {
		item := dashboardToolGroupItem{
			CanonicalName: toolName,
			Name:          localName(toolName),
			Server:        "Unknown",
		}
		if tool, err := s.mcpService.GetTool(toolName); err == nil {
			item.Description = tool.Description
			if server, serverErr := s.mcpService.GetToolParentServer(toolName); serverErr == nil {
				item.Server = server.Name
			}
		}
		tools = append(tools, item)
	}

	prompts := make([]dashboardToolGroupItem, 0, len(promptNames))
	for _, promptName := range promptNames {
		item := dashboardToolGroupItem{
			CanonicalName: promptName,
			Name:          localName(promptName),
			Server:        "Unknown",
		}
		if prompt, err := s.mcpService.GetPrompt(promptName); err == nil {
			item.Description = prompt.Description
			if server, serverErr := s.mcpService.GetPromptParentServer(promptName); serverErr == nil {
				item.Server = server.Name
			}
		}
		prompts = append(prompts, item)
	}

	resources := make([]dashboardToolGroupItem, 0, len(resourceURIs))
	for _, uri := range resourceURIs {
		item := dashboardToolGroupItem{
			CanonicalName: uri,
			Name:          uri,
			Server:        "Unknown",
		}
		if resource, err := s.mcpService.GetResource(uri); err == nil {
			item.Name = resource.Name
			item.Description = resource.Description
			item.Server = resource.Server.Name
		}
		resources = append(resources, item)
	}

	skills := make([]dashboardToolGroupItem, 0, len(skillNames))
	if store := s.mcpService.SkillStore(); store != nil {
		for _, skillName := range skillNames {
			item := dashboardToolGroupItem{
				CanonicalName: skillName,
				Name:          skillName,
				Server:        mcp.SkillsServerName,
			}
			if sk, err := store.Get(skillName); err == nil {
				item.Description = sk.Description
			}
			skills = append(skills, item)
		}
	} else {
		for _, skillName := range skillNames {
			skills = append(skills, dashboardToolGroupItem{
				CanonicalName: skillName,
				Name:          skillName,
				Server:        mcp.SkillsServerName,
			})
		}
	}

	endpoints := getToolGroupEndpoints(c, group.Name)

	return dashboardToolGroup{
		Name:                   group.Name,
		Description:            group.Description,
		ToolCount:              len(tools),
		PromptCount:            len(prompts),
		ResourceCount:          len(resources),
		SkillCount:             len(skills),
		Tools:                  tools,
		Prompts:                prompts,
		Resources:              resources,
		Skills:                 skills,
		StreamableHTTPEndpoint: endpoints.StreamableHTTPEndpoint,
		SSEEndpoint:            endpoints.SSEEndpoint,
		SSEMessageEndpoint:     endpoints.SSEMessageEndpoint,
	}, nil
}

func localName(canonical string) string {
	parts := strings.SplitN(canonical, "__", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return canonical
}
