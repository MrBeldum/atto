package app

import (
	"context"
	"fmt"

	"github.com/sebastianrcnt/atto/mcp"
	"github.com/sebastianrcnt/atto/tui"
)

// Choices of the approval prompt.
const (
	mcpAllow    = "Allow"
	mcpDeny     = "Deny"
	mcpAllowAll = "Allow all for this project"
)

// askMCPApprovals asks, one server after another, about the project MCP
// servers that wait for approval (the project's .mcp.json brings them, so
// the user decides, as for project extensions). Servers asked about once
// are not asked again this run; the Loaded block shows the others with
// how to approve them. Runs on the UI goroutine.
func (a *App) askMCPApprovals() {
	if a.mcp == nil || a.modal != nil {
		return
	}
	infos, _ := a.mcp.Servers(context.Background())
	for _, in := range infos {
		if in.Status != mcp.NeedsApproval {
			continue
		}
		key := in.Name + "#" + in.Hash
		if a.mcpAsked[key] {
			continue
		}
		if a.mcpAsked == nil {
			a.mcpAsked = map[string]bool{}
		}
		a.mcpAsked[key] = true
		a.askMCPApproval(in)
		return
	}
}

func (a *App) askMCPApproval(in mcp.Info) {
	l := &tui.SelectList{Title: fmt.Sprintf("This project wants to start MCP server %s: %s. Allow?", in.Name, in.Target)}
	for _, c := range []string{mcpAllow, mcpDeny, mcpAllowAll} {
		l.Items = append(l.Items, tui.SelectItem{Label: c, Value: c})
	}
	done := func(choice string) {
		a.closeModal()
		var err error
		switch choice {
		case mcpAllow:
			err = a.mcp.Approve(in.Name)
		case mcpAllowAll:
			err = a.mcp.ApproveAll(in.Name)
		case mcpDeny:
			err = a.mcp.Deny(in.Name)
		}
		if err != nil {
			a.errorNotice(err)
		}
		a.askMCPApprovals() // the next one, if any
	}
	l.OnCancel = func() { // esc: not now; the server stays unapproved
		a.closeModal()
		a.askMCPApprovals()
	}
	l.OnSelect = func(it tui.SelectItem) { done(it.Value) }
	a.openModal(l)
}
