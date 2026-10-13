package toolgroup

import (
	"fmt"

	"github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"github.com/mcpjungle/mcpjungle/pkg/util"
)

type groupMembership struct {
	tools     []string
	prompts   []string
	resources []string
	skills    []string
}

func (s *ToolGroupService) resolveGroupMembership(group *model.ToolGroup) (groupMembership, error) {
	var empty groupMembership
	tools, err := group.ResolveEffectiveTools(s.mcpService)
	if err != nil {
		return empty, fmt.Errorf("failed to resolve effective tools: %w", err)
	}
	prompts, err := group.GetPrompts()
	if err != nil {
		return empty, fmt.Errorf("failed to get included prompts: %w", err)
	}
	resources, err := group.GetResources()
	if err != nil {
		return empty, fmt.Errorf("failed to get included resources: %w", err)
	}
	skills, err := group.GetSkills()
	if err != nil {
		return empty, fmt.Errorf("failed to get included skills: %w", err)
	}
	return groupMembership{
		tools:     tools,
		prompts:   prompts,
		resources: resources,
		skills:    skills,
	}, nil
}

func (m groupMembership) empty() bool {
	return len(m.tools) == 0 && len(m.prompts) == 0 && len(m.resources) == 0 && len(m.skills) == 0
}

func (s *ToolGroupService) populateGroupServers(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	membership groupMembership,
	strict bool,
) error {
	for _, name := range membership.tools {
		if err := s.addToolToGroupServers(mcpServer, sseMcpServer, name, strict); err != nil {
			return err
		}
	}
	for _, name := range membership.prompts {
		if err := s.addPromptToGroupServers(mcpServer, sseMcpServer, name, strict); err != nil {
			return err
		}
	}
	for _, uri := range membership.resources {
		if err := s.addResourceToGroupServers(mcpServer, sseMcpServer, uri, strict); err != nil {
			return err
		}
	}
	if err := s.attachSkillsToGroupServers(mcpServer, sseMcpServer, membership.skills, strict); err != nil {
		return err
	}
	return nil
}

func (s *ToolGroupService) addToolToGroupServers(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	name string,
	strict bool,
) error {
	tool, exists := s.mcpService.GetToolInstance(name)
	if !exists {
		if !strict {
			return nil
		}
		return fmt.Errorf("tool %s does not exist or is disabled: %w", name, apierrors.ErrInvalidInput)
	}
	parentServer, err := s.mcpService.GetToolParentServer(name)
	if err != nil {
		return fmt.Errorf("failed to get parent MCP server of the tool %s: %w", name, err)
	}
	if parentServer.Transport == types.TransportSSE {
		sseMcpServer.AddTool(tool, s.mcpService.MCPProxyToolCallHandler)
	} else {
		mcpServer.AddTool(tool, s.mcpService.MCPProxyToolCallHandler)
	}
	return nil
}

func (s *ToolGroupService) addPromptToGroupServers(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	name string,
	strict bool,
) error {
	prompt, parent, err := s.mcpService.BuildEnabledPrompt(name)
	if err != nil {
		if !strict {
			return nil
		}
		return fmt.Errorf("prompt %s does not exist or is disabled: %w", name, apierrors.ErrInvalidInput)
	}
	if parent.Transport == types.TransportSSE {
		sseMcpServer.AddPrompt(prompt, s.mcpService.MCPProxyPromptCallHandler)
	} else {
		mcpServer.AddPrompt(prompt, s.mcpService.MCPProxyPromptCallHandler)
	}
	return nil
}

func (s *ToolGroupService) addResourceToGroupServers(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	uri string,
	strict bool,
) error {
	resource, parent, err := s.mcpService.BuildEnabledResource(uri)
	if err != nil {
		if !strict {
			return nil
		}
		return fmt.Errorf("resource %s does not exist or is disabled: %w", uri, apierrors.ErrInvalidInput)
	}
	if parent.Transport == types.TransportSSE {
		sseMcpServer.AddResource(resource, s.mcpService.MCPProxyResourceCallHandler)
	} else {
		mcpServer.AddResource(resource, s.mcpService.MCPProxyResourceCallHandler)
	}
	return nil
}

func (s *ToolGroupService) attachSkillsToGroupServers(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	skillNames []string,
	strict bool,
) error {
	if len(skillNames) == 0 {
		return nil
	}
	names := skillNames
	if !strict {
		names = existingSkillNames(s.mcpService, skillNames)
		if len(names) == 0 {
			return nil
		}
	}
	if err := s.mcpService.AttachSkillsToServer(mcpServer, names); err != nil {
		if !strict {
			return nil
		}
		return err
	}
	if err := s.mcpService.AttachSkillsToServer(sseMcpServer, names); err != nil {
		if !strict {
			return nil
		}
		return err
	}
	return nil
}

func existingSkillNames(mcpService *mcp.MCPService, skillNames []string) []string {
	store := mcpService.SkillStore()
	if store == nil {
		return nil
	}
	out := make([]string, 0, len(skillNames))
	for _, name := range skillNames {
		if _, err := store.Get(name); err == nil {
			out = append(out, name)
		}
	}
	return out
}

func (s *ToolGroupService) applyMembershipDiff(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	oldMembership, newMembership groupMembership,
) error {
	toolsAdded, toolsRemoved := util.DiffTools(oldMembership.tools, newMembership.tools)
	promptsAdded, promptsRemoved := util.DiffTools(oldMembership.prompts, newMembership.prompts)
	resourcesAdded, resourcesRemoved := util.DiffTools(oldMembership.resources, newMembership.resources)
	skillsAdded, skillsRemoved := util.DiffTools(oldMembership.skills, newMembership.skills)

	for _, name := range toolsRemoved {
		if err := s.removeToolFromGroupServers(mcpServer, sseMcpServer, name); err != nil {
			return err
		}
	}
	for _, name := range promptsRemoved {
		if err := s.removePromptFromGroupServers(mcpServer, sseMcpServer, name); err != nil {
			return err
		}
	}
	if len(resourcesRemoved) > 0 {
		mcpServer.DeleteResources(resourcesRemoved...)
		sseMcpServer.DeleteResources(resourcesRemoved...)
	}

	for _, name := range toolsAdded {
		if err := s.addToolToGroupServers(mcpServer, sseMcpServer, name, true); err != nil {
			return err
		}
	}
	for _, name := range promptsAdded {
		if err := s.addPromptToGroupServers(mcpServer, sseMcpServer, name, true); err != nil {
			return err
		}
	}
	for _, uri := range resourcesAdded {
		if err := s.addResourceToGroupServers(mcpServer, sseMcpServer, uri, true); err != nil {
			return err
		}
	}

	if len(skillsAdded) > 0 || len(skillsRemoved) > 0 {
		s.mcpService.DetachSkillsFromServer(mcpServer, oldMembership.skills)
		s.mcpService.DetachSkillsFromServer(sseMcpServer, oldMembership.skills)
		if err := s.attachSkillsToGroupServers(mcpServer, sseMcpServer, newMembership.skills, true); err != nil {
			return err
		}
	}

	return nil
}

func (s *ToolGroupService) removeToolFromGroupServers(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	name string,
) error {
	parentServer, err := s.mcpService.GetToolParentServer(name)
	if err != nil {
		return fmt.Errorf("failed to get parent MCP server of the tool %s: %w", name, err)
	}
	if parentServer.Transport == types.TransportSSE {
		sseMcpServer.DeleteTools(name)
	} else {
		mcpServer.DeleteTools(name)
	}
	return nil
}

func (s *ToolGroupService) removePromptFromGroupServers(
	mcpServer *server.MCPServer,
	sseMcpServer *server.MCPServer,
	name string,
) error {
	parentServer, err := s.mcpService.GetPromptParentServer(name)
	if err != nil {
		mcpServer.DeletePrompts(name)
		sseMcpServer.DeletePrompts(name)
		return nil
	}
	if parentServer.Transport == types.TransportSSE {
		sseMcpServer.DeletePrompts(name)
	} else {
		mcpServer.DeletePrompts(name)
	}
	return nil
}

func membershipChanged(oldMembership, newMembership groupMembership) bool {
	toolsAdded, toolsRemoved := util.DiffTools(oldMembership.tools, newMembership.tools)
	promptsAdded, promptsRemoved := util.DiffTools(oldMembership.prompts, newMembership.prompts)
	resourcesAdded, resourcesRemoved := util.DiffTools(oldMembership.resources, newMembership.resources)
	skillsAdded, skillsRemoved := util.DiffTools(oldMembership.skills, newMembership.skills)
	return len(toolsAdded)+len(toolsRemoved)+len(promptsAdded)+len(promptsRemoved)+
		len(resourcesAdded)+len(resourcesRemoved)+len(skillsAdded)+len(skillsRemoved) > 0
}
