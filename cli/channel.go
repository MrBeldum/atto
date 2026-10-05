package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sebastianrcnt/atto/update"
)

// RunChannel implements "atto channel [stable|edge]". The channel is part
// of the binary (see update.Channel), so switching installs the other
// channel's binary; nothing is saved anywhere.
func RunChannel(args []string, out io.Writer) error {
	exe, err := thisBinary()
	if err != nil {
		return err
	}
	return runChannel(args, out, update.Current(), exe)
}

func runChannel(args []string, out io.Writer, cur, exe string) error {
	const usage = "usage: atto channel [" + update.Stable + "|" + update.Edge + "]"
	if len(args) > 1 {
		return fmt.Errorf("%s", usage)
	}
	if len(args) == 0 {
		fmt.Fprintf(out, "atto %s\n", update.Describe())
		if update.Channel == "" {
			fmt.Fprintf(out, "This build has no update channel. Switch with: atto channel %s|%s\n", update.Stable, update.Edge)
		}
		return nil
	}
	target := args[0]
	if target != update.Stable && target != update.Edge {
		return fmt.Errorf("unknown channel %q; %s", target, usage)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rel, err := update.Latest(ctx, target)
	if err != nil {
		return err
	}
	// Moving to another channel installs its binary even when that is a
	// lower version (edge to stable); `atto update` never does.
	install, downgrade := update.Plan(rel, cur, target != update.Channel)
	if !install {
		fmt.Fprintf(out, "atto %s is already the latest %s release.\n", cur, target)
		return nil
	}
	if err := installTo(ctx, out, rel, cur, exe); err != nil {
		return err
	}
	if downgrade || target != update.Channel {
		fmt.Fprintf(out, "Switched to %s: atto %s → %s\n", target, cur, rel.Version)
	} else {
		fmt.Fprintf(out, "atto %s installed.\n", rel.Version)
	}
	fmt.Fprintln(out, "Running sessions keep the old version until restarted.")
	return nil
}
