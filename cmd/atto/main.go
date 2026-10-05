// Command atto is a terminal coding harness.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/sebastianrcnt/atto/app"
	"github.com/sebastianrcnt/atto/cli"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/server"
	"github.com/sebastianrcnt/atto/update"
)

const usage = `atto — a terminal coding harness

usage:
  atto [flags]                      interactive session
  atto [flags] "prompt"             interactive session, starting with this message
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

func subcommandNames() []string {
	var names []string
	for k := range subcommands() {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// subcommands maps each subcommand to its implementation.
func subcommands() map[string]func([]string, io.Writer) error {
	return map[string]func([]string, io.Writer) error{
		"history":    cli.RunHistory,
		"auth":       cli.RunAuth,
		"models":     cli.RunModels,
		"job":        cli.RunJob,
		"monitor":    cli.RunMonitor,
		"timer":      cli.RunTimer,
		"sleep":      cli.RunSleep,
		"goal":       cli.RunGoal,
		"update":     cli.RunUpdate,
		"_supervise": cli.RunSupervise,
		"login":      cli.RunLogin,
		"logout":     cli.RunLogout,
		"serve": func(args []string, out io.Writer) error {
			provider.UserAgent = "github.com/sebastianrcnt/atto/" + update.Current()
			return server.RunHTTP(update.Current(), args, out)
		},
		"app-server": func([]string, io.Writer) error {
			provider.UserAgent = "github.com/sebastianrcnt/atto/" + update.Current()
			return server.RunStdio(update.Current())
		},
	}
}

// unknownCommand reports whether a lone argument is a mistyped subcommand
// rather than a prompt, and what to print for it. A prompt is more than one
// word or contains a space; one bare word is far likelier a typo.
func unknownCommand(positional []string, known []string) (msg string, ok bool) {
	if len(positional) != 1 || strings.ContainsAny(positional[0], " \t\n") {
		return "", false
	}
	w := positional[0]
	if sug := closestCommand(w, known); sug != "" {
		return fmt.Sprintf("atto: unknown command %q. Did you mean %q?", w, sug), true
	}
	return fmt.Sprintf("atto: unknown command %q (see atto -h)", w), true
}

// commandAliases are common synonyms too far from the real name for edit
// distance to catch ("upgrade" is 3 edits from "update").
var commandAliases = map[string]string{"upgrade": "update"}

// closestCommand returns the known name within edit distance 2 of w, or "".
// Hidden names (leading underscore) are never suggested.
func closestCommand(w string, known []string) string {
	if to := commandAliases[w]; to != "" && slices.Contains(known, to) {
		return to
	}
	best, bd := "", 3
	for _, k := range known {
		if strings.HasPrefix(k, "_") {
			continue
		}
		if d := editDistance(w, k); d < bd || (d == bd && k < best) {
			best, bd = k, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// initialPrompt joins positional words into the first message, so
// atto fix the build and atto "fix the build" start the same session.
func initialPrompt(positional []string) string { return strings.Join(positional, " ") }

func main() {
	update.Cleanup()
	if len(os.Args) > 1 {
		refuseNested(os.Args[1])
		sub := subcommands()[os.Args[1]]
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
		fmt.Println("atto", update.Current())
		return
	}
	provider.UserAgent = "github.com/sebastianrcnt/atto/" + update.Current()
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
			prompt, err = cli.ReadPromptInput(positional) // a goal needs no prompt
		}
		if err == nil {
			err = cli.RunPrint(cli.PrintOptions{
				Prompt: prompt, Model: *model, Effort: *effort, Format: *format, Partial: *partial,
				Verbose: *verbose, MaxSteps: *maxSteps, Continue: *cont, Resume: *sessionID, NoSave: *noSave,
				Goal: *goalObj, GoalBudget: *goalBudget,
			})
		}
	} else {
		if msg, ok := unknownCommand(positional, subcommandNames()); ok {
			fmt.Fprintln(os.Stderr, msg)
			os.Exit(2)
		}
		err = app.Run(app.Options{Prompt: initialPrompt(positional), Inline: *inline, Continue: *cont, Resume: *resume, Model: *model, Session: *sessionID, Effort: *effort})
	}
	if errors.Is(err, cli.ErrPrintFailed) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "atto:", err)
		os.Exit(1)
	}
}
