package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"

	"github.com/sebastianrcnt/atto/auth"
	"github.com/sebastianrcnt/atto/config"
)

// RunLogin implements "atto login openai": Sign in with ChatGPT.
func RunLogin(args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "openai" {
		return fmt.Errorf("usage: atto login openai   (Sign in with ChatGPT)")
	}
	id, err := config.DeviceID()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	stdin := bufio.NewReader(os.Stdin)
	cred, err := auth.New(id).Login(ctx, auth.UI{
		ShowURL: func(url string) {
			fmt.Fprintf(out, "Open this URL in your browser to sign in with ChatGPT:\n\n%s\n\n", url)
			if openBrowser(url) != nil {
				fmt.Fprintln(out, "(could not open a browser; open the URL yourself)")
			}
			fmt.Fprintln(out, "Waiting for the browser. If the page cannot reach this machine, paste the final redirect URL here:")
		},
		Notice:     func(msg string) { fmt.Fprintln(out, msg) },
		ReadPasted: func() (string, error) { return stdin.ReadString('\n') },
	})
	if err != nil {
		return err
	}
	if err := config.SetOAuth("openai", cred); err != nil {
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

// openBrowser asks the OS to open url; failure is not fatal.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
