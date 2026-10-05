package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sebastianrcnt/atto/update"
)

// RunUpdate implements "atto update [-check]".
func RunUpdate(args []string, out io.Writer) error {
	fs := newFlags("update")
	checkOnly := fs.Bool("check", false, "only report whether a newer release exists")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("usage: atto update [-check]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tag, err := update.Latest(ctx)
	if err != nil {
		return err
	}
	cur := update.Current()
	if !update.Newer(tag, cur) && cur != "dev" {
		fmt.Fprintln(out, latestLine(cur))
		return nil
	}
	if *checkOnly {
		fmt.Fprintln(out, availableLine(cur, tag))
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if how := update.Managed(exe); how != "" {
		return fmt.Errorf("%s was installed by another tool; update it with: %s", exe, how)
	}
	fmt.Fprintf(out, "Updating %s from %s to %s…\n", exe, cur, tag)
	if err := update.InstallRelease(ctx, tag, exe); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("%w\n%s isn't writable; reinstall with: %s", err, filepath.Dir(exe), update.Install)
		}
		return err
	}
	fmt.Fprintln(out, updatedLine(cur, tag))
	fmt.Fprintln(out, "Running sessions keep the old version until restarted.")
	return nil
}

// The three outcomes of an update check, kept as functions so the wording is
// tested without a network. cur is "dev" for builds without a release tag.

func latestLine(cur string) string { return fmt.Sprintf("atto %s (latest)", cur) }

func availableLine(cur, tag string) string {
	return fmt.Sprintf("atto %s \u00b7 %s is available (atto update)", cur, tag)
}

func updatedLine(cur, tag string) string { return fmt.Sprintf("Updated atto %s \u2192 %s", cur, tag) }
