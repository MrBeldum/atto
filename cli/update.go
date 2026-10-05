package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/update"
)

// RunUpdate implements "atto update [-check] [-channel stable|edge]".
func RunUpdate(args []string, out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	return runUpdate(args, out, update.Current(), exe)
}

func runUpdate(args []string, out io.Writer, cur, exe string) error {
	const usage = "usage: atto update [-check] [-channel stable|edge]"
	fs := newFlags("update")
	checkOnly := fs.Bool("check", false, "only report whether a newer release exists")
	channelFlag := fs.String("channel", "", "stable (tagged releases) or edge (every push to main); saved to settings.json")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return fmt.Errorf("%s", usage)
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	channel := update.Channel(settings.UpdateChannel)
	explicit := *channelFlag != ""
	if explicit {
		if *channelFlag != update.Stable && *channelFlag != update.Edge {
			return fmt.Errorf("unknown channel %q; %s", *channelFlag, usage)
		}
		if *channelFlag != settings.UpdateChannel {
			// Save before touching the network so the choice sticks even
			// if the check fails.
			if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
				return err
			}
			if err := config.UpdateSettings(map[string]any{"updateChannel": *channelFlag}); err != nil {
				return err
			}
			fmt.Fprintf(out, "Update channel set to %s.\n", *channelFlag)
		}
		channel = *channelFlag
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rel, err := update.Latest(ctx, channel)
	if err != nil {
		return err
	}
	install, downgrade := update.Plan(rel, cur, explicit && channel == update.Stable)
	if !install {
		fmt.Fprintf(out, "atto %s is the latest %s release.\n", cur, channel)
		return nil
	}
	if *checkOnly {
		if downgrade {
			fmt.Fprintf(out, "atto %s is the latest stable release (you have %s). Switch with: atto update -channel stable\n", rel.Version, cur)
		} else {
			fmt.Fprintf(out, "atto %s is available (you have %s). Update with: atto update\n", rel.Version, cur)
		}
		return nil
	}
	if how := update.Managed(exe); how != "" {
		return fmt.Errorf("%s was installed by another tool; update it with: %s", exe, how)
	}
	fmt.Fprintf(out, "Updating %s from %s to %s…\n", exe, cur, rel.Version)
	if err := update.InstallRelease(ctx, rel.Tag, exe); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("%w\n%s isn't writable; reinstall with: %s", err, filepath.Dir(exe), update.Install)
		}
		return err
	}
	if downgrade {
		fmt.Fprintf(out, "Switched to stable: atto %s → %s\n", cur, rel.Version)
	} else {
		fmt.Fprintf(out, "atto %s installed.\n", rel.Version)
	}
	fmt.Fprintln(out, "Running sessions keep the old version until restarted.")
	return nil
}
