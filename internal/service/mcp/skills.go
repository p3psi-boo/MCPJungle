package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/service/skills"
	"github.com/mcpjungle/mcpjungle/internal/telemetry"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"gorm.io/gorm"
)

// SkillsServerName is the name of the built-in virtual MCP server that exposes Agent Skills.
// Skill tools and prompts are named `skills__<name>`, and in enterprise mode a client needs
// this name (or `*`) in its allow list to see and use them.
const SkillsServerName = "skills"

const (
	skillsListToolName     = "list_skills"
	skillsGetToolName      = "get_skill"
	skillsReadFileToolName = "read_skill_file"

	skillPromptTaskArg = "task"
)

type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type listSkillsResult struct {
	Skills []skillSummary `json:"skills"`
}

// RegisterSkills exposes the given skills through the MCP proxy servers as the built-in
// `skills` server: three read-only tools for progressive disclosure (list, get, read file)
// and one prompt per skill.
// It must be called at most once, after the proxy servers have been initialized.
func (m *MCPService) RegisterSkills(store *skills.Store) error {
	if store == nil {
		return fmt.Errorf("skills store is nil")
	}

	var existing model.McpServer
	err := m.db.Where("name = ?", SkillsServerName).First(&existing).Error
	if err == nil {
		return fmt.Errorf(
			"an MCP server named %q is already registered, which conflicts with the built-in skills server;"+
				" deregister it or disable skills",
			SkillsServerName,
		)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to check for MCP server %q: %w", SkillsServerName, err)
	}

	m.skillStore = store

	for _, t := range m.skillTools() {
		m.mcpProxyServer.AddTool(t.tool, t.handler)
		m.sseMcpProxyServer.AddTool(t.tool, t.handler)
	}

	for _, sk := range store.List() {
		prompt := mcp.NewPrompt(
			mergeServerPromptNames(SkillsServerName, sk.Name),
			mcp.WithPromptDescription(sk.Description),
			mcp.WithArgument(
				skillPromptTaskArg,
				mcp.ArgumentDescription("Optional description of the task to accomplish using this skill"),
			),
		)
		m.mcpProxyServer.AddPrompt(prompt, m.skillPromptHandler)
		m.sseMcpProxyServer.AddPrompt(prompt, m.skillPromptHandler)
	}

	return nil
}

// isReservedServerName reports whether name cannot be used for a user-registered MCP server.
func (m *MCPService) isReservedServerName(name string) bool {
	return m.skillStore != nil && name == SkillsServerName
}

type skillTool struct {
	tool    mcp.Tool
	handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

func (m *MCPService) skillTools() []skillTool {
	return []skillTool{
		{
			tool: mcp.NewTool(
				mergeServerToolNames(SkillsServerName, skillsListToolName),
				mcp.WithDescription(
					"List the Agent Skills available on this gateway. A skill is a package of expert "+
						"instructions (and optional supporting files) for a specific kind of task. "+
						"Call "+mergeServerToolNames(SkillsServerName, skillsGetToolName)+
						" to load a skill's instructions when its description matches your task.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
			),
			handler: m.withSkillToolMetrics(skillsListToolName, m.handleListSkills),
		},
		{
			tool: mcp.NewTool(
				mergeServerToolNames(SkillsServerName, skillsGetToolName),
				mcp.WithDescription(m.getSkillToolDescription()),
				mcp.WithString("name", mcp.Required(), mcp.Description("Name of the skill to load")),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
			),
			handler: m.withSkillToolMetrics(skillsGetToolName, m.handleGetSkill),
		},
		{
			tool: mcp.NewTool(
				mergeServerToolNames(SkillsServerName, skillsReadFileToolName),
				mcp.WithDescription(
					"Read a text file bundled with a skill (for example a reference document, template "+
						"or script mentioned in the skill's instructions). Use the file paths listed by "+
						mergeServerToolNames(SkillsServerName, skillsGetToolName)+".",
				),
				mcp.WithString("name", mcp.Required(), mcp.Description("Name of the skill")),
				mcp.WithString(
					"path",
					mcp.Required(),
					mcp.Description("Path of the file relative to the skill directory, eg- references/api.md"),
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
			),
			handler: m.withSkillToolMetrics(skillsReadFileToolName, m.handleReadSkillFile),
		},
	}
}

// getSkillToolDescription embeds the skill catalog in the tool description so that clients
// discover available skills from tools/list alone, without an extra round trip.
func (m *MCPService) getSkillToolDescription() string {
	var b strings.Builder
	b.WriteString(
		"Load the full instructions of an Agent Skill, along with the list of files bundled with it. " +
			"Use this before working on a task that matches one of the skills below, then follow the instructions.",
	)
	list := m.skillStore.List()
	if len(list) == 0 {
		b.WriteString("\n\nNo skills are currently available.")
		return b.String()
	}
	b.WriteString("\n\nAvailable skills:")
	for _, sk := range list {
		fmt.Fprintf(&b, "\n- %s: %s", sk.Name, sk.Description)
	}
	return b.String()
}

func (m *MCPService) withSkillToolMetrics(
	toolName string,
	handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error),
) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := authorizeProxyServerAccess(ctx, SkillsServerName); err != nil {
			return nil, err
		}
		started := time.Now()
		res, err := handler(ctx, request)
		outcome := telemetry.ToolCallOutcomeSuccess
		if err != nil || (res != nil && res.IsError) {
			outcome = telemetry.ToolCallOutcomeError
		}
		m.metrics.RecordToolCall(ctx, SkillsServerName, toolName, outcome, time.Since(started))
		return res, err
	}
}

func (m *MCPService) handleListSkills(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	list := m.skillStore.List()
	result := listSkillsResult{Skills: make([]skillSummary, len(list))}
	for i, sk := range list {
		result.Skills[i] = skillSummary{Name: sk.Name, Description: sk.Description}
	}
	return mcp.NewToolResultJSON(result)
}

func (m *MCPService) handleGetSkill(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sk, err := m.skillStore.Get(name)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	text, err := renderSkill(sk)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(text), nil
}

func (m *MCPService) handleReadSkillFile(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	path, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sk, err := m.skillStore.Get(name)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := sk.ReadFile(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(content), nil
}

func (m *MCPService) skillPromptHandler(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	if err := authorizeProxyServerAccess(ctx, SkillsServerName); err != nil {
		return nil, err
	}

	started := time.Now()
	outcome := telemetry.PromptCallOutcomeSuccess
	serverName, skillName, ok := splitServerPromptName(request.Params.Name)
	defer func() {
		m.metrics.RecordPromptCall(ctx, SkillsServerName, skillName, outcome, time.Since(started))
	}()

	if !ok || serverName != SkillsServerName {
		outcome = telemetry.PromptCallOutcomeError
		return nil, fmt.Errorf("prompt %s is not a skill prompt: %w", request.Params.Name, apierrors.ErrInvalidInput)
	}
	sk, err := m.skillStore.Get(skillName)
	if err != nil {
		outcome = telemetry.PromptCallOutcomeError
		return nil, err
	}
	text, err := renderSkill(sk)
	if err != nil {
		outcome = telemetry.PromptCallOutcomeError
		return nil, err
	}
	if task := strings.TrimSpace(request.Params.Arguments[skillPromptTaskArg]); task != "" {
		text += "\n\n---\n\nTask: " + task
	}

	return mcp.NewGetPromptResult(
		sk.Description,
		[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(text))},
	), nil
}

// renderSkill produces the text handed to the model when a skill is activated.
func renderSkill(sk *skills.Skill) (string, error) {
	instructions, err := sk.Instructions()
	if err != nil {
		return "", fmt.Errorf("failed to read skill %q: %w", sk.Name, err)
	}
	files, truncated, err := sk.Files()
	if err != nil {
		return "", fmt.Errorf("failed to list files of skill %q: %w", sk.Name, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Skill: %s\n\n%s\n", sk.Name, sk.Description)
	if len(files) > 0 {
		fmt.Fprintf(
			&b,
			"\nFiles bundled with this skill (read them with %s when the instructions reference them):\n",
			mergeServerToolNames(SkillsServerName, skillsReadFileToolName),
		)
		for _, f := range files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		if truncated {
			fmt.Fprintf(&b, "- ... (list truncated at %d files)\n", skills.MaxListedFiles)
		}
	}
	b.WriteString("\n---\n\n")
	b.WriteString(instructions)
	return b.String(), nil
}
