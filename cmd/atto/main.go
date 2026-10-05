// Command atto is a terminal coding harness.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sebastianrcnt/atto/app"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/server"
	"github.com/sebastianrcnt/atto/update"
)

const usage = `atto — a terminal coding harness

usage:
  atto [flags]                      interactive session
  atto -p [flags] "prompt"          run one prompt and print the result
  cat file | atto -p "explain"      stdin is appended to the prompt
  atto models [refresh]             list available models
  atto auth set <provider>          store an API key
  atto login openai                 sign in with ChatGPT (subscription)
  atto logout <provider>            remove stored credentials
  atto history grep|show ...        search a session transcript
  atto job|monitor|timer|sleep ...  background jobs and wake-ups (atto job for details)
  atto goal [complete|blocked|set]  the session goal (set one with /goal or -goal)
  atto update [-check]              install the latest release
  atto serve [-listen addr]         JSON-RPC over HTTP + SSE, with a web client
  atto app-server                   JSON-RPC over stdio (JSON lines)

flags:
`

// nestedRefused are the commands an atto agent may not run from its shell:
// starting another agent (which would recurse and spend tokens unseen) or
// changing credentials. "" is atto itself (interactive or -p).
var nestedRefused = map[string]bool{"": true, "serve": true, "app-server": true, "login": true, "logout": true, "auth": true, "update": true}

func refuseNested(cmd string) {
	if !config.InAgent() || !nestedRefused[cmd] {
		return
	}
	what := "start another atto agent"
	switch cmd {
	case "login", "logout", "auth":
		what = "change atto's credentials"
	case "update":
		what = "replace the atto binary"
	}
	fmt.Fprintf(os.Stderr, "atto: commands run by an atto agent can't %s (%s is set). Do the work in this session instead; for background work use atto job.\n", what, config.EnvAgent)
	os.Exit(2)
}

func main() {
	update.Cleanup()
	if len(os.Args) > 1 {
		refuseNested(os.Args[1])
		sub := map[string]func([]string, io.Writer) error{
			"history":    app.RunHistory,
			"auth":       app.RunAuth,
			"models":     app.RunModels,
			"job":        app.RunJob,
			"monitor":    app.RunMonitor,
			"timer":      app.RunTimer,
			"sleep":      app.RunSleep,
			"goal":       app.RunGoal,
			"update":     app.RunUpdate,
			"_supervise": app.RunSupervise,
			"login":      app.RunLogin,
			"logout":     app.RunLogout,
			"serve": func(args []string, out io.Writer) error {
				provider.UserAgent = "github.com/sebastianrcnt/atto/" + app.Version
				return server.RunHTTP(app.Version, args, out)
			},
			"app-server": func([]string, io.Writer) error {
				provider.UserAgent = "github.com/sebastianrcnt/atto/" + app.Version
				return server.RunStdio(app.Version)
			},
		}[os.Args[1]]
		if sub != nil {
			if err := sub(os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	fs := flag.NewFlagSet("atto", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}
	showVersion := fs.Bool("version", false, "print version and exit")
	print := fs.Bool("p", false, "print mode: run the prompt non-interactively and exit")
	model := fs.String("m", "", "model to use, as provider/id (see: atto models)")
	effort := fs.String("effort", "", "reasoning effort for this run")
	cont := fs.Bool("c", false, "continue the most recent session in this directory")
	sessionID := fs.String("session", "", "continue the session with this ID")
	resume := fs.Bool("resume", false, "pick a saved session to resume (interactive)")
	inline := fs.Bool("inline", false, "render inline in the main screen instead of fullscreen")
	format := fs.String("output-format", "text", "print mode output: text, json or stream-json")
	partial := fs.Bool("include-partial", false, "stream-json: also emit text and reasoning deltas")
	verbose := fs.Bool("v", false, "print mode: show tool activity on stderr")
	maxSteps := fs.Int("max-steps", 0, "print mode: stop after this many model calls")
	noSave := fs.Bool("no-save", false, "print mode: do not save the run as a session")
	goalObj := fs.String("goal", "", "print mode: keep working until this objective is done")
	goalBudget := fs.String("goal-budget", "", "print mode: token budget for -goal, e.g. 200k")

	// Allow flags before and after the prompt: atto -p "fix it" -m x.
	var positional []string
	args := os.Args[1:]
	for {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}

	if *showVersion {
		fmt.Println("atto", app.Version)
		return
	}
	provider.UserAgent = "github.com/sebastianrcnt/atto/" + app.Version
	refuseNested("")

	var err error
	if *print {
		switch *format {
		case "text", "json", "stream-json":
		default:
			fmt.Fprintf(os.Stderr, "atto: unknown --output-format %q\n", *format)
			os.Exit(2)
		}
		var prompt string
		if *goalObj == "" || len(positional) > 0 {
			prompt, err = app.ReadPromptInput(positional) // a goal needs no prompt
		}
		if err == nil {
			err = app.RunPrint(app.PrintOptions{
				Prompt: prompt, Model: *model, Effort: *effort, Format: *format, Partial: *partial,
				Verbose: *verbose, MaxSteps: *maxSteps, Continue: *cont, Resume: *sessionID, NoSave: *noSave,
				Goal: *goalObj, GoalBudget: *goalBudget,
			})
		}
	} else {
		if len(positional) > 0 {
			fmt.Fprintln(os.Stderr, "atto: a prompt argument needs -p (print mode)")
			os.Exit(2)
		}
		err = app.Run(app.Options{Inline: *inline, Continue: *cont, Resume: *resume, Model: *model, Session: *sessionID, Effort: *effort})
	}
	if errors.Is(err, app.ErrPrintFailed) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "atto:", err)
		os.Exit(1)
	}
}
