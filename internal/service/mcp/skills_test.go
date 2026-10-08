package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/service/skills"
	"github.com/mcpjungle/mcpjungle/internal/telemetry"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func newSkillsTestService(t *testing.T) (*MCPService, *skills.Store) {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "pdf")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, skills.SkillFileName),
		[]byte("---\nname: pdf\ndescription: Fill and merge PDF files.\n---\n# PDF\n\nRun scripts/fill.py.\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "fill.py"), []byte("print('fill')\n"), 0o644))

	store, err := skills.Load([]string{root})
	require.NoError(t, err)

	service := &MCPService{
		db:                setupTestDBForProxyAdditional(t),
		mcpProxyServer:    mcpserver.NewMCPServer("proxy", "test", mcpserver.WithToolCapabilities(true)),
		sseMcpProxyServer: mcpserver.NewMCPServer("sse", "test", mcpserver.WithToolCapabilities(true)),
		metrics:           telemetry.NewNoopCustomMetrics(),
	}
	require.NoError(t, service.RegisterSkills(store))
	return service, store
}

func devCtx() context.Context {
	return context.WithValue(context.Background(), "mode", model.ModeDev)
}

func callSkillTool(t *testing.T, s *MCPService, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	srvTool := s.mcpProxyServer.GetTool(mergeServerToolNames(SkillsServerName, tool))
	require.NotNil(t, srvTool, "tool %s not registered", tool)
	req := mcp.CallToolRequest{}
	req.Params.Name = srvTool.Tool.Name
	req.Params.Arguments = args
	res, err := srvTool.Handler(devCtx(), req)
	require.NoError(t, err)
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	return text.Text
}

func TestRegisterSkills_RegistersToolsAndPrompts(t *testing.T) {
	s, _ := newSkillsTestService(t)

	for _, srv := range []*mcpserver.MCPServer{s.mcpProxyServer, s.sseMcpProxyServer} {
		tools := srv.ListTools()
		assert.Contains(t, tools, "skills__list_skills")
		assert.Contains(t, tools, "skills__get_skill")
		assert.Contains(t, tools, "skills__read_skill_file")
		assert.Contains(t, tools["skills__get_skill"].Tool.Description, "- pdf: Fill and merge PDF files.")
	}
	assert.True(t, s.isReservedServerName(SkillsServerName))
}

func TestSkillTools(t *testing.T) {
	s, _ := newSkillsTestService(t)

	res := callSkillTool(t, s, skillsListToolName, nil)
	assert.False(t, res.IsError)
	var listed listSkillsResult
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &listed))
	assert.Equal(t, []skillSummary{{Name: "pdf", Description: "Fill and merge PDF files."}}, listed.Skills)

	res = callSkillTool(t, s, skillsGetToolName, map[string]any{"name": "pdf"})
	assert.False(t, res.IsError)
	text := resultText(t, res)
	assert.Contains(t, text, "# Skill: pdf")
	assert.Contains(t, text, "- scripts/fill.py")
	assert.Contains(t, text, "Run scripts/fill.py.")
	assert.NotContains(t, text, "description: Fill")

	res = callSkillTool(t, s, skillsGetToolName, map[string]any{"name": "missing"})
	assert.True(t, res.IsError)

	res = callSkillTool(t, s, skillsReadFileToolName, map[string]any{"name": "pdf", "path": "scripts/fill.py"})
	assert.False(t, res.IsError)
	assert.Equal(t, "print('fill')\n", resultText(t, res))

	res = callSkillTool(t, s, skillsReadFileToolName, map[string]any{"name": "pdf", "path": "../../etc/passwd"})
	assert.True(t, res.IsError)
}

func TestSkillPromptHandler(t *testing.T) {
	s, _ := newSkillsTestService(t)

	req := mcp.GetPromptRequest{}
	req.Params.Name = "skills__pdf"
	req.Params.Arguments = map[string]string{skillPromptTaskArg: "merge a.pdf and b.pdf"}
	res, err := s.skillPromptHandler(devCtx(), req)
	require.NoError(t, err)
	assert.Equal(t, "Fill and merge PDF files.", res.Description)
	require.Len(t, res.Messages, 1)
	assert.Equal(t, mcp.RoleUser, res.Messages[0].Role)
	text := res.Messages[0].Content.(mcp.TextContent).Text
	assert.Contains(t, text, "Run scripts/fill.py.")
	assert.Contains(t, text, "Task: merge a.pdf and b.pdf")

	req.Params.Name = "skills__missing"
	_, err = s.skillPromptHandler(devCtx(), req)
	assert.Error(t, err)
}

func TestSkills_EnterpriseAccessControl(t *testing.T) {
	s, _ := newSkillsTestService(t)

	allowList, _ := json.Marshal([]string{"other"})
	client := &model.McpClient{Name: "c", AllowList: datatypes.JSON(allowList)}
	ctx := context.WithValue(context.Background(), "mode", model.ModeEnterprise)
	ctx = context.WithValue(ctx, "client", client)

	tool := s.mcpProxyServer.GetTool("skills__list_skills")
	_, err := tool.Handler(ctx, mcp.CallToolRequest{})
	assert.Error(t, err)
	assert.Empty(t, ProxyToolFilter(ctx, []mcp.Tool{tool.Tool}))

	promptReq := mcp.GetPromptRequest{}
	promptReq.Params.Name = "skills__pdf"
	_, err = s.skillPromptHandler(ctx, promptReq)
	assert.Error(t, err)

	allowList, _ = json.Marshal([]string{SkillsServerName})
	client.AllowList = datatypes.JSON(allowList)
	_, err = tool.Handler(ctx, mcp.CallToolRequest{})
	assert.NoError(t, err)
	assert.Len(t, ProxyToolFilter(ctx, []mcp.Tool{tool.Tool}), 1)
}

func TestRegisterSkills_ConflictsWithRegisteredServer(t *testing.T) {
	db := setupTestDBForProxyAdditional(t)
	require.NoError(t, db.Create(createStreamableHTTPTestServer(t, SkillsServerName, "http://localhost:1")).Error)

	s := &MCPService{
		db:                db,
		mcpProxyServer:    mcpserver.NewMCPServer("proxy", "test"),
		sseMcpProxyServer: mcpserver.NewMCPServer("sse", "test"),
		metrics:           telemetry.NewNoopCustomMetrics(),
	}
	store, err := skills.Load(nil)
	require.NoError(t, err)
	assert.Error(t, s.RegisterSkills(store))
}

func TestRegisterMcpServer_RejectsReservedSkillsName(t *testing.T) {
	s, _ := newSkillsTestService(t)
	err := s.registerMcpServer(context.Background(), createStreamableHTTPTestServer(t, SkillsServerName, "http://localhost:1"), false)
	assert.ErrorIs(t, err, apierrors.ErrInvalidInput)
}
