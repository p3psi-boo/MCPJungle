package management

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/internal/service/toolgroup"
	"github.com/mcpjungle/mcpjungle/internal/telemetry"
	"github.com/mcpjungle/mcpjungle/pkg/testhelpers"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"github.com/stretchr/testify/assert"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type fixture struct {
	db         *gorm.DB
	proxy      *server.MCPServer
	mcpService *mcp.MCPService
	groups     *toolgroup.ToolGroupService
}

// newFixture registers a "git" server with tools "commit" and "push", and a "time" server with "now".
// The servers are never contacted: the tools are loaded from the database.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := testhelpers.CreateTestDB()
	testhelpers.AssertNoError(t, err)
	testhelpers.AssertNoError(t, db.AutoMigrate(
		&model.McpServer{}, &model.Tool{}, &model.ToolGroup{}, &model.Prompt{}, &model.Resource{},
	))

	seedServer(t, db, "git", `{"url":"http://localhost:1/mcp","bearer_token":"secret-token"}`, "commit", "push")
	seedServer(t, db, "time", `{"url":"http://localhost:2/mcp"}`, "now")

	newProxy := func() *server.MCPServer {
		return server.NewMCPServer(
			"test proxy", "0.0.1",
			server.WithToolCapabilities(true),
			server.WithToolFilter(mcp.ProxyToolFilter),
		)
	}
	proxy := newProxy()
	mcpService, err := mcp.NewMCPService(&mcp.ServiceConfig{
		DB:                      db,
		McpProxyServer:          proxy,
		SseMcpProxyServer:       newProxy(),
		Metrics:                 telemetry.NewNoopCustomMetrics(),
		McpServerInitReqTimeout: 1,
	})
	testhelpers.AssertNoError(t, err)
	groups, err := toolgroup.NewToolGroupService(db, mcpService)
	testhelpers.AssertNoError(t, err)
	testhelpers.AssertNoError(t, NewService(mcpService, groups).Register())

	return &fixture{db: db, proxy: proxy, mcpService: mcpService, groups: groups}
}

func seedServer(t *testing.T, db *gorm.DB, name, config string, tools ...string) {
	t.Helper()
	s := &model.McpServer{
		Name:      name,
		Transport: types.TransportStreamableHTTP,
		Enabled:   true,
		Config:    datatypes.JSON(config),
	}
	testhelpers.AssertNoError(t, db.Create(s).Error)
	for _, tool := range tools {
		testhelpers.AssertNoError(t, db.Create(&model.Tool{
			ServerID:    s.ID,
			Name:        tool,
			Enabled:     true,
			Description: tool + " tool",
			InputSchema: datatypes.JSON(`{"type":"object"}`),
		}).Error)
	}
}

func devCtx() context.Context {
	return context.WithValue(context.Background(), "mode", model.ModeDev)
}

func enterpriseCtx(allowList string) context.Context {
	ctx := context.WithValue(context.Background(), "mode", model.ModeEnterprise)
	return context.WithValue(ctx, "client", &model.McpClient{Name: "agent", AllowList: datatypes.JSON(allowList)})
}

func (f *fixture) rpc(t *testing.T, ctx context.Context, method string, params any) mcpgo.JSONRPCMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	testhelpers.AssertNoError(t, err)
	return f.proxy.HandleMessage(ctx, raw)
}

func (f *fixture) listToolNames(t *testing.T, ctx context.Context) []string {
	t.Helper()
	var result mcpgo.ListToolsResult
	decodeResult(t, f.rpc(t, ctx, "tools/list", map[string]any{}), &result)
	names := make([]string, len(result.Tools))
	for i, tool := range result.Tools {
		names[i] = tool.Name
	}
	sort.Strings(names)
	return names
}

// call invokes a management tool and decodes its JSON result into out (if non-nil).
// It returns the error text when the tool reports a failure.
func (f *fixture) call(t *testing.T, ctx context.Context, tool string, args map[string]any, out any) string {
	t.Helper()
	msg := f.rpc(t, ctx, "tools/call", map[string]any{"name": toolName(tool), "arguments": args})
	if rpcErr, ok := msg.(mcpgo.JSONRPCError); ok {
		return rpcErr.Error.Message
	}
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	decodeResult(t, msg, &result)
	text := result.Content[0].Text
	if result.IsError {
		return text
	}
	if out != nil {
		testhelpers.AssertNoError(t, json.Unmarshal([]byte(text), out))
	}
	return ""
}

func decodeResult(t *testing.T, msg mcpgo.JSONRPCMessage, out any) {
	t.Helper()
	resp, ok := msg.(mcpgo.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected a JSON-RPC result, got %#v", msg)
	}
	raw, err := json.Marshal(resp.Result)
	testhelpers.AssertNoError(t, err)
	testhelpers.AssertNoError(t, json.Unmarshal(raw, out))
}

func groupTools(t *testing.T, f *fixture, group string) []string {
	t.Helper()
	srv, ok := f.groups.GetToolGroupMCPServer(group)
	if !ok {
		return nil
	}
	var names []string
	for name := range srv.ListTools() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestRegisterExposesManagementTools(t *testing.T) {
	f := newFixture(t)

	names := f.listToolNames(t, devCtx())
	for _, tool := range []string{
		listServersToolName, setServerEnabledToolName, listToolsToolName, setToolsEnabledToolName,
		listToolGroupsToolName, getToolGroupToolName, createToolGroupToolName, updateToolGroupToolName,
		deleteToolGroupToolName,
	} {
		testhelpers.AssertTrue(t, contains(names, toolName(tool)), "missing tool "+toolName(tool))
	}
	testhelpers.AssertTrue(t, contains(names, "git__commit"), "upstream tools should still be listed")
}

func TestEnterpriseModeRequiresExplicitAllowListEntry(t *testing.T) {
	f := newFixture(t)

	wildcard := enterpriseCtx(`["*"]`)
	for _, name := range f.listToolNames(t, wildcard) {
		testhelpers.AssertFalse(t, strings.HasPrefix(name, mcp.ManagementServerName+"__"), "wildcard client sees "+name)
	}
	errText := f.call(t, wildcard, listServersToolName, nil, nil)
	testhelpers.AssertStringContains(t, errText, "not authorized")

	explicit := enterpriseCtx(fmt.Sprintf(`["%s"]`, mcp.ManagementServerName))
	names := f.listToolNames(t, explicit)
	testhelpers.AssertTrue(t, contains(names, toolName(listServersToolName)), "explicit client should see management tools")
	testhelpers.AssertFalse(t, contains(names, "git__commit"), "explicit client should not see unrelated servers")
	assert.Equal(t, "", f.call(t, explicit, listServersToolName, nil, nil))
}

func TestListServersDoesNotExposeConfig(t *testing.T) {
	f := newFixture(t)

	raw, err := json.Marshal(f.rpc(t, devCtx(), "tools/call", map[string]any{"name": toolName(listServersToolName)}))
	testhelpers.AssertNoError(t, err)
	testhelpers.AssertStringNotContains(t, string(raw), "secret-token")

	var out struct {
		Servers []serverSummary `json:"servers"`
	}
	assert.Equal(t, "", f.call(t, devCtx(), listServersToolName, nil, &out))
	assert.Equal(t, 2, len(out.Servers))
	assert.Equal(t, "git", out.Servers[0].Name)
}

func TestListTools(t *testing.T) {
	f := newFixture(t)

	var out struct {
		Tools []toolSummary `json:"tools"`
	}
	assert.Equal(t, "", f.call(t, devCtx(), listToolsToolName, map[string]any{"server": "git"}, &out))
	assert.Equal(t, 2, len(out.Tools))
	assert.Equal(t, "git__commit", out.Tools[0].Name)

	testhelpers.AssertStringContains(t, f.call(t, devCtx(), listToolsToolName, map[string]any{"server": "nope"}, nil), "not found")
}

func TestToolGroupLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := devCtx()

	var created toolGroupView
	errText := f.call(t, ctx, createToolGroupToolName, map[string]any{
		"name":             "dev",
		"description":      "coding tools",
		"included_servers": []string{"git"},
		"excluded_tools":   []string{"git__push"},
		"included_tools":   []string{"time__now"},
	}, &created)
	assert.Equal(t, "", errText)
	assert.Equal(t, []string{"git__commit", "time__now"}, created.EffectiveTools)
	assert.Equal(t, "/v0/groups/dev/mcp", created.Endpoints.StreamableHTTP)
	assert.Equal(t, []string{"git__commit", "time__now"}, groupTools(t, f, "dev"))

	testhelpers.AssertStringContains(
		t,
		f.call(t, ctx, createToolGroupToolName, map[string]any{"name": "empty", "included_tools": []string{}}, nil),
		"at least one tool",
	)

	// a partial update keeps the fields that are not passed
	var updated toolGroupView
	errText = f.call(t, ctx, updateToolGroupToolName, map[string]any{"name": "dev", "excluded_tools": []string{}}, &updated)
	assert.Equal(t, "", errText)
	assert.Equal(t, "coding tools", updated.Description)
	assert.Equal(t, []string{"git"}, updated.IncludedServers)
	assert.Equal(t, []string{"git__commit", "git__push", "time__now"}, updated.EffectiveTools)
	assert.Equal(t, []string{"git__commit", "git__push", "time__now"}, groupTools(t, f, "dev"))

	var listed struct {
		ToolGroups []toolGroupView `json:"tool_groups"`
	}
	assert.Equal(t, "", f.call(t, ctx, listToolGroupsToolName, nil, &listed))
	assert.Equal(t, 1, len(listed.ToolGroups))
	assert.Equal(t, "dev", listed.ToolGroups[0].Name)

	assert.Equal(t, "", f.call(t, ctx, deleteToolGroupToolName, map[string]any{"name": "dev"}, nil))
	assert.Equal(t, 0, len(groupTools(t, f, "dev")))
	testhelpers.AssertStringContains(t, f.call(t, ctx, deleteToolGroupToolName, map[string]any{"name": "dev"}, nil), "not found")
	testhelpers.AssertStringContains(t, f.call(t, ctx, getToolGroupToolName, map[string]any{"name": "dev"}, nil), "not found")
}

func TestSetToolsEnabledUpdatesGroups(t *testing.T) {
	f := newFixture(t)
	ctx := devCtx()

	assert.Equal(t, "", f.call(t, ctx, createToolGroupToolName, map[string]any{
		"name": "dev", "included_servers": []string{"git"},
	}, nil))

	var out struct {
		Tools []string `json:"tools"`
	}
	assert.Equal(t, "", f.call(t, ctx, setToolsEnabledToolName, map[string]any{
		"names": []string{"git__push"}, "enabled": false,
	}, &out))
	assert.Equal(t, []string{"git__push"}, out.Tools)
	assert.Equal(t, []string{"git__commit"}, groupTools(t, f, "dev"))
	testhelpers.AssertFalse(t, contains(f.listToolNames(t, ctx), "git__push"), "disabled tool should be hidden")

	assert.Equal(t, "", f.call(t, ctx, setToolsEnabledToolName, map[string]any{
		"names": []string{"git"}, "enabled": true,
	}, &out))
	assert.Equal(t, []string{"git__push"}, out.Tools)
	assert.Equal(t, []string{"git__commit", "git__push"}, groupTools(t, f, "dev"))
}

func TestSetServerEnabled(t *testing.T) {
	f := newFixture(t)
	ctx := devCtx()

	assert.Equal(t, "", f.call(t, ctx, setServerEnabledToolName, map[string]any{"name": "time", "enabled": false}, nil))
	s, err := f.mcpService.GetMcpServer("time")
	testhelpers.AssertNoError(t, err)
	testhelpers.AssertFalse(t, s.Enabled, "server should be disabled")
	testhelpers.AssertFalse(t, contains(f.listToolNames(t, ctx), "time__now"), "tools of a disabled server should be hidden")
}

func TestRegisterFailsWhenServerNameIsTaken(t *testing.T) {
	db, err := testhelpers.CreateTestDB()
	testhelpers.AssertNoError(t, err)
	testhelpers.AssertNoError(t, db.AutoMigrate(
		&model.McpServer{}, &model.Tool{}, &model.ToolGroup{}, &model.Prompt{}, &model.Resource{},
	))
	seedServer(t, db, mcp.ManagementServerName, `{"url":"http://localhost:1/mcp"}`)

	newProxy := func() *server.MCPServer { return server.NewMCPServer("p", "0.0.1", server.WithToolCapabilities(true)) }
	mcpService, err := mcp.NewMCPService(&mcp.ServiceConfig{
		DB: db, McpProxyServer: newProxy(), SseMcpProxyServer: newProxy(), Metrics: telemetry.NewNoopCustomMetrics(),
	})
	testhelpers.AssertNoError(t, err)
	groups, err := toolgroup.NewToolGroupService(db, mcpService)
	testhelpers.AssertNoError(t, err)

	err = NewService(mcpService, groups).Register()
	testhelpers.AssertError(t, err)
	testhelpers.AssertStringContains(t, err.Error(), "already registered")
}

func TestRegisteringReservedServerNameFails(t *testing.T) {
	f := newFixture(t)

	err := f.mcpService.RegisterMcpServerWithOAuthSupport(
		context.Background(),
		nil,
		&model.McpServer{
			Name:      mcp.ManagementServerName,
			Transport: types.TransportStreamableHTTP,
			Config:    datatypes.JSON(`{"url":"http://localhost:1/mcp"}`),
		},
		false,
		"",
	)
	testhelpers.AssertError(t, err)
	testhelpers.AssertStringContains(t, err.Error(), "reserved")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
