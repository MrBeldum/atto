package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/mcp"
	"github.com/sebastianrcnt/atto/mcp/mcptest"
	"github.com/sebastianrcnt/atto/session"
)

func TestMain(m *testing.M) {
	mcptest.ServeIfRequested()
	os.Exit(m.Run())
}

func fakeServer(args ...string) string {
	cmd, env := mcptest.Command()
	c, _ := json.Marshal(mcp.ServerConfig{Command: cmd, Args: args, Env: env})
	return string(c)
}

// openMCP is open with the MCP servers loaded the way the front ends do,
// before the prompt is built.
func openMCP(t *testing.T, cwd string) *agent.Agent {
	t.Helper()
	ag, _ := open(t, cwd)
	m := LoadMCP(ag)
	t.Cleanup(func() { m.Close() })
	ag.SetStart(time.Date(2026, 1, 2, 3, 0, 0, 0, time.Local))
	return ag
}

func count(t *testing.T, ag *agent.Agent, server string) string {
	t.Helper()
	res, err := MCPOf(ag).Call(context.Background(), server, "count", nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Text
}

const mcpLinePrefix = "MCP servers are available through"

func TestPromptHasNoMCPLineWithoutServers(t *testing.T) {
	_, _, cwd := project(t)
	ag := openMCP(t, cwd)
	if MCPOf(ag) == nil {
		t.Fatal("no manager")
	}
	if strings.Contains(ag.SystemPrompt(), mcpLinePrefix) {
		t.Fatal("the prompt mentions MCP though nothing is configured")
	}
	l := Collect(ag, nil, FromDefault, FromDefault)
	for _, r := range l.Summary() {
		if r.Label == "MCP" {
			t.Errorf("the collapsed block has an MCP row for no servers: %+v", r)
		}
	}
	// The expanded view says how to add one.
	var found bool
	for _, s := range l.Details() {
		if s.Title == "MCP servers" {
			found = len(s.Rows) == 1 && s.Rows[0].Label == "none" && strings.Contains(s.Rows[0].Text, "atto mcp add")
		}
	}
	if !found {
		t.Error("no MCP section in the details")
	}
}

func TestPromptLineAndLoadedBlockForMCPServers(t *testing.T) {
	atto, repo, cwd := project(t)
	writeFile(t, filepath.Join(atto, "mcp.json"), `{"mcpServers": {"zeta": `+fakeServer()+`, "alpha": {"type": "http", "url": "https://example.invalid/mcp"}}}`)
	writeFile(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {"shared": {"command": "npx", "args": ["-y", "thing"]}}}`)
	writeFile(t, mcp.Path(mcp.ScopeLocal, repo), `{"mcpServers": {"broken": {"args": ["no command"]}}}`)

	ag := openMCP(t, cwd)
	prompt := ag.SystemPrompt()
	line := mcpLinePrefix + ` "atto mcp tools [server [tool]]" and "atto mcp call <server> <tool> '<json args>'" (configured: alpha, broken, shared, zeta).`
	if strings.Count(prompt, mcpLinePrefix) != 1 || !strings.Contains(prompt, "\n"+line+"\n") {
		t.Fatalf("the prompt lacks the line:\n%s", prompt)
	}

	l := Collect(ag, nil, FromDefault, FromDefault)
	var summary string
	for _, r := range l.Summary() {
		if r.Label == "MCP" {
			summary = r.Text
		}
	}
	if summary != "2: alpha, zeta; 1 failed; 1 needs approval" {
		t.Fatalf("summary %q", summary)
	}
	var rows []string
	for _, s := range l.Details() {
		if s.Title == "MCP servers" {
			for _, r := range s.Rows {
				rows = append(rows, r.Label+" "+r.Text)
			}
		}
	}
	got := strings.Join(rows, "\n")
	for _, want := range []string{
		"alpha user · http · https://example.invalid/mcp · not started",
		"zeta user · stdio · ",
		"shared project · stdio · npx -y thing · needs approval: atto mcp approve shared",
		"broken local · stdio · ", "failed: a stdio server needs a command",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("details lack %q:\n%s", want, got)
		}
	}
	if len(l.Warnings) != 2 || !strings.Contains(strings.Join(l.Warnings, "\n"), "project MCP server shared is not started until approved: atto mcp approve shared") {
		t.Errorf("warnings %q", l.Warnings)
	}
	if !strings.Contains(l.Text(), "MCP servers") || !strings.Contains(l.Text(), "one line naming") {
		t.Errorf("atto context text lacks the MCP parts:\n%s", l.Text())
	}
	for _, c := range l.Config {
		if strings.HasSuffix(c.Path, "mcp.json") && !c.Exists && !strings.Contains(c.Path, "repo") {
			t.Errorf("config file %s should exist", c.Path)
		}
	}

	// The prompt does not depend on what runs or on approvals, only on the
	// configuration: it is the same string after use, approval and a rebuild.
	count(t, ag, "zeta")
	if err := MCPOf(ag).Approve("shared"); err != nil {
		t.Fatal(err)
	}
	if ag.Reload() || ag.SystemPrompt() != prompt {
		t.Fatal("running a server or approving one changed the prompt")
	}
}

func TestReloadRestartsChangedMCPServersAndReportsThem(t *testing.T) {
	atto, _, cwd := project(t)
	path := filepath.Join(atto, "mcp.json")
	writeFile(t, path, `{"mcpServers": {"keep": `+fakeServer()+`, "change": `+fakeServer()+`}}`)
	ag := openMCP(t, cwd)
	prompt := ag.SystemPrompt()
	l := Collect(ag, nil, FromDefault, FromDefault)
	count(t, ag, "keep")
	count(t, ag, "change")

	// Nothing changed: the servers keep running and the prompt is untouched.
	r, err := Reload(ag, "s1", "", l)
	if err != nil {
		t.Fatal(err)
	}
	if r.PromptChanged || ag.SystemPrompt() != prompt {
		t.Fatal("a reload without changes rebuilt the prompt")
	}
	for _, c := range r.Changes {
		if c.What == "MCP server" {
			t.Errorf("unexpected change %s", c)
		}
	}
	if got := count(t, ag, "keep"); got != "2" {
		t.Fatalf("keep restarted by an unchanged reload: count %s", got)
	}

	// One changed, one added.
	writeFile(t, path, `{"mcpServers": {"keep": `+fakeServer()+`, "change": `+fakeServer("-x")+`, "fresh": `+fakeServer()+`}}`)
	r, err = Reload(ag, "s1", "", r.Loaded)
	if err != nil {
		t.Fatal(err)
	}
	var changes []string
	for _, c := range r.Changes {
		if c.What == "MCP server" {
			changes = append(changes, c.String())
		}
	}
	if want := []string{"changed MCP server change", "added MCP server fresh"}; !slices.Equal(changes, want) {
		t.Fatalf("changes %q, want %q", changes, want)
	}
	if !r.PromptChanged || !strings.Contains(ag.SystemPrompt(), "(configured: change, fresh, keep)") {
		t.Fatalf("the prompt did not take the new server list:\n%s", ag.SystemPrompt())
	}
	if got := count(t, ag, "keep"); got != "3" {
		t.Errorf("keep restarted: count %s", got)
	}
	if got := count(t, ag, "change"); got != "1" {
		t.Errorf("change did not restart: count %s", got)
	}
	if !strings.Contains(r.ForModel(), "added MCP server fresh") {
		t.Errorf("the model is not told: %s", r.ForModel())
	}

	// Removing the last servers removes the line.
	writeFile(t, path, `{"mcpServers": {}}`)
	if _, err = Reload(ag, "s1", "", r.Loaded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ag.SystemPrompt(), mcpLinePrefix) {
		t.Error("the line stayed after the servers went")
	}
}

func TestBindPublishesTheSessionEndpoint(t *testing.T) {
	atto, _, cwd := project(t)
	writeFile(t, filepath.Join(atto, "mcp.json"), `{"mcpServers": {"fake": `+fakeServer()+`}}`)
	ag := openMCP(t, cwd)

	file := session.New(cwd)
	t.Cleanup(func() { file.Close() })
	Bind(ag, nil, file, time.Now(), false)
	b, err := mcp.Dial(file.ID)
	if err != nil {
		t.Fatalf("the agent's shell cannot reach the session: %v", err)
	}
	res, err := b.Call(context.Background(), "fake", "count", nil)
	if err != nil || res.Text != "1" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := count(t, ag, "fake"); got != "2" {
		t.Fatalf("not the same server: count %s", got)
	}
	// The commands the agent runs carry the ID that finds it.
	if !slices.Contains(Env(file.ID), "ATTO_SESSION_ID="+file.ID) {
		t.Fatal("no session id in the agent's environment")
	}
}

func TestRepoLocalMCPFileIsIgnoredAndReported(t *testing.T) {
	atto, repo, cwd := project(t)
	writeFile(t, filepath.Join(repo, ".atto", "mcp.json"), `{"mcpServers": {"evil": {"command": "touch", "args": ["pwned"]}}}`)
	ag := openMCP(t, cwd)
	if names := MCPOf(ag).Names(); len(names) != 0 {
		t.Fatalf("a repository's .atto/mcp.json was read: %v", names)
	}
	if strings.Contains(ag.SystemPrompt(), mcpLinePrefix) {
		t.Fatal("the prompt names a server from the ignored file")
	}
	l := Collect(ag, nil, FromDefault, FromDefault)
	var row string
	for _, s := range l.Summary() {
		if s.Label == "MCP" {
			row = s.Text
		}
	}
	if !strings.HasPrefix(row, "ignored: ") {
		t.Errorf("summary %q", row)
	}
	text := l.Text()
	local := filepath.ToSlash(mcp.Path(mcp.ScopeLocal, repo))
	if !strings.Contains(text, "is ignored") || !strings.Contains(text, "atto mcp add -scope local") ||
		strings.Contains(local, filepath.ToSlash(repo)) || !strings.HasPrefix(local, filepath.ToSlash(atto)) {
		t.Errorf("local path %s should be under %s, not the repo; text:\n%s", local, atto, text)
	}
	if len(l.Warnings) == 0 {
		t.Error("no warning")
	}
}
