// Command atto is a terminal coding harness.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"atto/app"
	"atto/provider"
)

const usage = `atto — a terminal coding harness

usage:
  atto [flags]                      interactive session
  atto -p [flags] "prompt"          run one prompt and print the result
  cat file | atto -p "explain"      stdin is appended to the prompt
  atto models [refresh]             list available models
  atto auth set <provider>          store an API key
  atto history grep|show ...        search a session transcript

flags:
`

func main() {
	if len(os.Args) > 1 {
		sub := map[string]func([]string, io.Writer) error{
			"history": app.RunHistory,
			"auth":    app.RunAuth,
			"models":  app.RunModels,
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
	provider.UserAgent = "atto/" + app.Version

	var err error
	if *print {
		switch *format {
		case "text", "json", "stream-json":
		default:
			fmt.Fprintf(os.Stderr, "atto: unknown --output-format %q\n", *format)
			os.Exit(2)
		}
		var prompt string
		if prompt, err = app.ReadPromptInput(positional); err == nil {
			err = app.RunPrint(app.PrintOptions{
				Prompt: prompt, Model: *model, Effort: *effort, Format: *format, Partial: *partial,
				Verbose: *verbose, MaxSteps: *maxSteps, Continue: *cont, Resume: *sessionID, NoSave: *noSave,
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
