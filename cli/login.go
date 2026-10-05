package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/sebastianrcnt/atto/auth"
	"github.com/sebastianrcnt/atto/config"
)

// RunLogin implements "atto login <provider>": the OAuth logins (pi's
// /login outside the TUI). API keys are saved with "atto auth set".
func RunLogin(args []string, out io.Writer) error {
	device := false
	var rest []string
	for _, a := range args {
		if a == "--device" || a == "-device" {
			device = true
		} else {
			rest = append(rest, a)
		}
	}
	var p *auth.OAuthProvider
	if len(rest) == 1 {
		p = auth.GetOAuthProvider(rest[0])
	}
	if p == nil {
		var b strings.Builder
		b.WriteString("usage: atto login <provider>   (or /login inside atto)\n\nproviders:\n")
		for _, o := range auth.OAuthProviders() {
			fmt.Fprintf(&b, "  %-14s %s\n", o.ID, o.Name)
		}
		b.WriteString("\natto login openai-codex --device uses a device code instead of the browser.\nAPI keys: atto auth set <provider>")
		return fmt.Errorf("%s", b.String())
	}
	id, err := config.DeviceID()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var cred auth.Credential
	if device {
		if p.ID != "openai-codex" {
			return fmt.Errorf("--device is only available for openai-codex")
		}
		cred, err = auth.NewCodexClient().LoginDeviceCode(ctx, func(code, url string) {
			fmt.Fprintf(out, "Open %s and enter the code %s\n", url, code)
		})
	} else {
		stdin := bufio.NewReader(os.Stdin)
		cred, err = p.Login(ctx, auth.UI{
			ShowURL: func(url string) {
				fmt.Fprintf(out, "Open this URL in your browser to sign in (%s):\n\n%s\n\n", p.Name, url)
				if auth.OpenBrowser(url) != nil {
					fmt.Fprintln(out, "(could not open a browser; open the URL yourself)")
				}
				fmt.Fprintln(out, "Waiting for the browser. If the page cannot reach this machine, paste the final redirect URL here:")
			},
			Notice:     func(msg string) { fmt.Fprintln(out, msg) },
			ReadPasted: func() (string, error) { return stdin.ReadString('\n') },
		}, id)
	}
	if err != nil {
		return err
	}
	if err := config.SetOAuth(p.ID, cred); err != nil {
		return err
	}
	fmt.Fprintf(out, "Signed in. Credentials saved to %s\n", config.AuthPath())
	return nil
}

// RunLogout implements "atto logout <provider>": remove stored credentials.
func RunLogout(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: atto logout <provider>")
	}
	found, err := config.RemoveAuth(args[0])
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(out, "No stored credentials for %s\n", args[0])
		return nil
	}
	fmt.Fprintf(out, "Removed credentials for %s from %s\n", args[0], config.AuthPath())
	return nil
}
