package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/goal"
)

const goalUsage = `usage:
  atto goal                         show the session's goal
  atto goal complete "<evidence>"   the goal is achieved (after verifying it)
  atto goal blocked "<reason>"      stalled: the same blocker for three goal turns in a row, needs the user
  atto goal pause "<why>"           only when the user explicitly asked to pause the goal
  atto goal set [-budget 50k] "<objective>"   set a goal (users only; refused inside atto)`

// RunGoal implements "atto goal". The model uses complete/blocked from its
// shell; the front end notices the change and stops continuing the goal.
func RunGoal(args []string, out io.Writer) error {
	fs := newFlags("goal")
	session := sessionFlag(fs)
	budget := fs.String("budget", "", "token budget, e.g. 50k")
	sub := "show"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%v\n%s", err, goalUsage)
	}
	if err := requireSession(*session); err != nil {
		return err
	}
	if config.InAgent() {
		// A model reports on its own goal only: it cannot set goals or
		// touch another session's.
		if sub == "set" {
			return fmt.Errorf("only the user sets goals (/goal in atto); report on the current goal with: atto goal complete|blocked \"...\"")
		}
		if *session != os.Getenv("ATTO_SESSION_ID") {
			return fmt.Errorf("-session can't be changed inside atto: a goal is reported from its own session")
		}
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	g, err := goal.Load(*session)
	if err != nil {
		return err
	}

	switch sub {
	case "show":
		if g == nil {
			fmt.Fprintln(out, "no goal")
			return nil
		}
		fmt.Fprintf(out, "status: %s\nusage: %s, %d turns\nobjective:\n%s\n", g.Status.Label(), g.Usage(), g.Turns, g.Objective)
		if g.Note != "" {
			fmt.Fprintf(out, "note: %s\n", g.Note)
		}
	case "complete", "blocked", "pause":
		if g == nil {
			return fmt.Errorf("there is no goal")
		}
		if g.Status != goal.Active && (g.Status != goal.BudgetLimited || sub == "pause") {
			return fmt.Errorf("the goal is %s, not active", g.Status.Label())
		}
		if text == "" {
			return fmt.Errorf("give the %s: atto goal %s \"...\"", map[string]string{"complete": "evidence", "blocked": "reason", "pause": "reason"}[sub], sub)
		}
		g.Status, g.Note = map[string]goal.Status{"complete": goal.Complete, "blocked": goal.Blocked, "pause": goal.Paused}[sub], text
		if err := goal.Save(*session, g); err != nil {
			return err
		}
		fmt.Fprintf(out, "goal marked %s. End your turn with a short summary for the user.\n", g.Status.Label())
	case "set":
		b := 0
		if *budget != "" {
			if b, err = goal.ParseBudget(*budget); err != nil {
				return err
			}
		}
		ng, err := goal.New(text, b)
		if err != nil {
			return err
		}
		if err := goal.Save(*session, ng); err != nil {
			return err
		}
		fmt.Fprintln(out, "goal set; atto continues it whenever the agent goes idle.")
	default:
		return fmt.Errorf("unknown subcommand %q\n%s", sub, goalUsage)
	}
	return nil
}
