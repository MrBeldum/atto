package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"atto/agent"
	"atto/config"
)

// RunPrint runs a single prompt non-interactively, streaming plain text to
// stdout and tool activity to stderr.
func RunPrint(prompt string) error {
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	models, err := config.LoadModels()
	if err != nil {
		return err
	}
	model, ok := models.Find(settings.DefaultProvider, settings.DefaultModel)
	if !ok {
		all := models.List()
		if len(all) == 0 {
			return fmt.Errorf("no models configured; add a provider to %s", config.ModelsPath())
		}
		model = all[0]
	}
	effort := settings.DefaultEffort
	if effort == "" {
		effort = "medium"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ag := agent.New(model, effort, cwd)
	var out io.Writer = os.Stdout
	err = ag.Run(ctx, prompt, func(ev any) {
		switch e := ev.(type) {
		case agent.TextDelta:
			fmt.Fprint(out, e.Text)
		case agent.ToolStart:
			fmt.Fprintf(os.Stderr, "\n● %s  $ %s\n", e.Args.Description, firstLine(e.Args.Command))
		case agent.ToolEnd:
			r := e.Result
			fmt.Fprintf(os.Stderr, "  └ exit %d · %s%s\n", r.ExitCode, fmtDur(r.Duration), map[bool]string{true: " · timed out"}[r.TimedOut])
		}
	})
	fmt.Fprintln(out)
	return err
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i] + " …"
		}
	}
	return s
}
