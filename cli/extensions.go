package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/core"
	"github.com/sebastianrcnt/atto/docs"
	"github.com/sebastianrcnt/atto/extensions"
)

const extensionsUsage = `usage: atto extensions [list [-json] | approve <name> | types | docs]

  list            the extensions a session here loads (the default), without
                  running them: user ones from ~/.atto/extensions, project ones
                  from <project>/.atto/extensions
  approve <name>  let the project extension <name> run, as its code is now;
                  a change to it needs approval again. Not from an agent's shell.
  types           print atto.d.ts, the API's TypeScript declarations
  docs            print the guide to writing extensions

A running session picks changes up with /reload (or atto reload).`

// RunExtensions implements "atto extensions".
func RunExtensions(args []string, out io.Writer) error {
	cmd := "list"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	switch cmd {
	case "list":
		fs := newFlags("extensions list")
		asJSON := fs.Bool("json", false, "")
		if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
			return fmt.Errorf("%s", extensionsUsage)
		}
		list := extensions.Inspect(cwd)
		if *asJSON {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(list)
		}
		if len(list) == 0 {
			fmt.Fprintf(out, "No extensions. Put .ts or .js files in %s or %s.\n",
				core.ShortPath(config.ExtensionsDir()), core.ShortPath(config.ProjectExtensionsDir("<project>")))
			return nil
		}
		for _, in := range list {
			text := in.Status + " · " + in.Source + " · " + core.ShortPath(in.Path)
			switch in.Status {
			case extensions.Failed:
				text = "failed: " + in.Error
			case extensions.NeedsApproval:
				text += " · approve with: atto extensions approve " + in.Name
			case extensions.Ready:
				text = "ok · " + in.Source + " · " + core.ShortPath(in.Path)
			}
			fmt.Fprintf(out, "%s  %s\n", in.Name, text)
		}
		return nil
	case "approve":
		if len(args) != 1 {
			return fmt.Errorf("%s", extensionsUsage)
		}
		if config.InAgent() {
			// Approval is the user's check on code a repository brings.
			return fmt.Errorf("atto: project extensions are approved by the user, not from an agent's shell (%s is set). Ask the user to run: atto extensions approve %s (or /extensions approve %s)", config.EnvAgent, args[0], args[0])
		}
		s, err := extensions.Approve(cwd, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Approved %s (%s). A running session loads it after /reload.\n", s.Name, core.ShortPath(s.Path))
		return nil
	case "types":
		_, err := io.WriteString(out, extensions.Types)
		return err
	case "docs":
		_, err := io.WriteString(out, docs.Extensions)
		return err
	}
	return fmt.Errorf("%s", extensionsUsage)
}
