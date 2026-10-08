package management

import (
	"context"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/internal/service/skills"
)

const (
	listSkillsToolName    = "list_skills"
	reloadSkillsToolName  = "reload_skills"
	previewSkillsToolName = "preview_skills"
	installSkillsToolName = "install_skills"
	updateSkillToolName   = "update_skill"
	removeSkillToolName   = "remove_skill"
)

const skillSourceDescription = "GitHub source: owner/repo, owner/repo/path, owner/repo@ref, " +
	"or a https://github.com/owner/repo/tree/<ref>/<path> URL"

type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Removable is true for skills installed at runtime, which can be updated or removed.
	Removable bool           `json:"removable"`
	Origin    *skills.Origin `json:"origin,omitempty"`
}

func newSkillSummary(sk *skills.Skill) skillSummary {
	return skillSummary{Name: sk.Name, Description: sk.Description, Removable: sk.Removable, Origin: sk.Origin}
}

// skillTools returns the skill management tools. Without a skill store there are none,
// and without an install directory only the tools that don't write skills are returned.
func (s *Service) skillTools() []server.ServerTool {
	store := s.mcpService.SkillStore()
	if store == nil {
		return nil
	}

	tools := []server.ServerTool{
		{
			Tool: mcpgo.NewTool(
				toolName(listSkillsToolName),
				mcpgo.WithDescription(
					"List the Agent Skills served by the gateway, with where each was installed from and whether "+
						"it can be updated or removed. Read a skill's instructions with "+
						mcp.SkillsServerName+"__get_skill.",
				),
				readOnly(),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleListSkills,
		},
		{
			Tool: mcpgo.NewTool(
				toolName(reloadSkillsToolName),
				mcpgo.WithDescription("Rescan the skills directories, picking up skills added, changed or deleted on disk."),
				mcpgo.WithDestructiveHintAnnotation(false),
				mcpgo.WithIdempotentHintAnnotation(true),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleReloadSkills,
		},
	}
	if store.InstallDir() == "" {
		return tools
	}

	return append(tools,
		server.ServerTool{
			Tool: mcpgo.NewTool(
				toolName(previewSkillsToolName),
				mcpgo.WithDescription(
					"List the skills found in a GitHub source without installing anything, including whether each "+
						"one is already installed.",
				),
				mcpgo.WithString("source", mcpgo.Required(), mcpgo.Description(skillSourceDescription)),
				readOnly(),
				mcpgo.WithOpenWorldHintAnnotation(true),
			),
			Handler: s.handlePreviewSkills,
		},
		server.ServerTool{
			Tool: mcpgo.NewTool(
				toolName(installSkillsToolName),
				mcpgo.WithDescription(
					"Download skills from GitHub and serve them to every client. Use "+
						toolName(previewSkillsToolName)+" first to see what a source contains.",
				),
				mcpgo.WithString("source", mcpgo.Required(), mcpgo.Description(skillSourceDescription)),
				mcpgo.WithArray(
					"skills",
					mcpgo.WithStringItems(),
					mcpgo.Description("Names of the skills to install; omit to install every skill in the source"),
				),
				mcpgo.WithBoolean("overwrite", mcpgo.Description("Replace skills that are already installed")),
				mcpgo.WithDestructiveHintAnnotation(true),
				mcpgo.WithIdempotentHintAnnotation(false),
				mcpgo.WithOpenWorldHintAnnotation(true),
			),
			Handler: s.handleInstallSkills,
		},
		server.ServerTool{
			Tool: mcpgo.NewTool(
				toolName(updateSkillToolName),
				mcpgo.WithDescription("Reinstall an installed skill from the GitHub source it was installed from."),
				mcpgo.WithString("name", mcpgo.Required(), mcpgo.Description("Name of the skill")),
				mcpgo.WithDestructiveHintAnnotation(true),
				mcpgo.WithIdempotentHintAnnotation(true),
				mcpgo.WithOpenWorldHintAnnotation(true),
			),
			Handler: s.handleUpdateSkill,
		},
		server.ServerTool{
			Tool: mcpgo.NewTool(
				toolName(removeSkillToolName),
				mcpgo.WithDescription(
					"Remove a skill installed at runtime. Skills provided by a skills directory can't be removed.",
				),
				mcpgo.WithString("name", mcpgo.Required(), mcpgo.Description("Name of the skill")),
				mcpgo.WithDestructiveHintAnnotation(true),
				mcpgo.WithIdempotentHintAnnotation(false),
				mcpgo.WithOpenWorldHintAnnotation(false),
			),
			Handler: s.handleRemoveSkill,
		},
	)
}

func (s *Service) handleListSkills(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	list := s.mcpService.SkillStore().List()
	result := make([]skillSummary, len(list))
	for i := range list {
		result[i] = newSkillSummary(&list[i])
	}
	return mcpgo.NewToolResultJSON(map[string]any{"skills": result})
}

func (s *Service) handleReloadSkills(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	store := s.mcpService.SkillStore()
	if err := store.Reload(); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return mcpgo.NewToolResultJSON(map[string]any{"skill_count": len(store.List())})
}

func (s *Service) handlePreviewSkills(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	source, err := request.RequireString("source")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	preview, err := s.mcpService.SkillStore().Preview(ctx, source)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if preview.Skills == nil {
		preview.Skills = []skills.Candidate{}
	}
	return mcpgo.NewToolResultJSON(preview)
}

func (s *Service) handleInstallSkills(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	source, err := request.RequireString("source")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	names := request.GetStringSlice("skills", nil)
	overwrite := request.GetBool("overwrite", false)

	installed, err := s.mcpService.SkillStore().Install(ctx, source, names, overwrite)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	result := make([]skillSummary, len(installed))
	for i := range installed {
		result[i] = newSkillSummary(&installed[i])
	}
	return mcpgo.NewToolResultJSON(map[string]any{"installed": result})
}

func (s *Service) handleUpdateSkill(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	sk, err := s.mcpService.SkillStore().Update(ctx, name)
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return mcpgo.NewToolResultJSON(newSkillSummary(sk))
}

func (s *Service) handleRemoveSkill(_ context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	if err := s.mcpService.SkillStore().Remove(name); err != nil {
		return mcpgo.NewToolResultError(err.Error()), nil
	}
	return mcpgo.NewToolResultJSON(map[string]any{"removed": name})
}
