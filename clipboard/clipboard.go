// Package clipboard reads and writes the OS clipboard. Releases are built
// without cgo, so instead of a native clipboard library (codex uses
// arboard) it shells out to the platform's tools. Every dependency is a
// field of Clipboard so tests can check which commands run without touching
// a real clipboard.
//
// Terminal-side copying (OSC 52) is not here: it needs the TUI's output and
// lives in the app.
package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Clipboard talks to the platform's clipboard tools.
type Clipboard struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Run runs a command and returns its stdout.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// RunStdin runs a command with stdin and discards stdout. A failure
	// carries the command's stderr when it printed any.
	RunStdin func(ctx context.Context, stdin string, name string, args ...string) error
	ReadFile func(string) ([]byte, error)
}

// System uses the real OS.
func System() Clipboard {
	return Clipboard{
		GOOS:     runtime.GOOS,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		RunStdin: func(ctx context.Context, stdin string, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdin = strings.NewReader(stdin)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				if msg := strings.TrimSpace(stderr.String()); msg != "" {
					return &commandError{msg}
				}
				return err
			}
			return nil
		},
		ReadFile: os.ReadFile,
	}
}

// WriteText puts text on the system clipboard.
func WriteText(text string) error { return System().WriteText(text) }

// ReadImage reads an image from the system clipboard; see Clipboard.ReadImage.
func ReadImage(ctx context.Context, accept func([]byte) error) ([]byte, error) {
	return System().ReadImage(ctx, accept)
}

type commandError struct{ msg string }

func (e *commandError) Error() string { return e.msg }

// ErrNoCopyTool means no clipboard write command is available.
var ErrNoCopyTool = &commandError{"no clipboard command (install wl-clipboard or xclip)"}

// ErrNoImage means the clipboard holds no image.
var ErrNoImage = errors.New("no image on the clipboard")

// copyCommand is the OS clipboard command for this platform, or nil.
func (c Clipboard) copyCommand() []string {
	have := func(name string) bool { _, err := c.LookPath(name); return err == nil }
	switch c.GOOS {
	case "darwin":
		return []string{"pbcopy"}
	case "windows":
		// Set-Clipboard reads base64 from stdin: console encodings would
		// mangle non-ASCII text passed directly.
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"Set-Clipboard -Value ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([Console]::In.ReadToEnd())))"}
	}
	switch {
	case c.Getenv("WAYLAND_DISPLAY") != "" && have("wl-copy"):
		return []string{"wl-copy"}
	case c.Getenv("DISPLAY") != "" && have("xclip"):
		return []string{"xclip", "-selection", "clipboard"}
	case c.Getenv("DISPLAY") != "" && have("xsel"):
		return []string{"xsel", "--clipboard", "--input"}
	}
	return nil
}

// WriteText puts text on the clipboard with the platform's command.
func (c Clipboard) WriteText(text string) error {
	args := c.copyCommand()
	if args == nil {
		return ErrNoCopyTool
	}
	input := text
	if c.GOOS == "windows" {
		input = base64.StdEncoding.EncodeToString([]byte(text))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.RunStdin(ctx, input, args[0], args[1:]...)
}

// Output forms a clipboard command can produce.
const (
	outRaw    = iota // image bytes
	outBase64        // base64 text (PowerShell)
	outHex           // AppleScript «data PNGf…» literal
	outPath          // a file path (a file copied in Finder)
)

type clipCmd struct {
	name string
	args []string
	out  int
}

// psScript prints the clipboard image as base64 PNG. Get-Clipboard -Format
// Image exists in Windows PowerShell 5.1; the Forms call covers pwsh 7.
const psScript = `$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Windows.Forms,System.Drawing; ` +
	`$img = $null; try { $img = Get-Clipboard -Format Image } catch {}; ` +
	`if ($img -eq $null) { $img = [System.Windows.Forms.Clipboard]::GetImage() }; ` +
	`if ($img -eq $null) { exit 1 }; $ms = New-Object System.IO.MemoryStream; ` +
	`$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); [Console]::Out.Write([Convert]::ToBase64String($ms.ToArray()))`

// commands lists what to try, in order, on this platform. A tool that is
// not installed is skipped.
func (c Clipboard) commands() []clipCmd {
	have := func(name string) bool { _, err := c.LookPath(name); return err == nil }
	var out []clipCmd
	add := func(cmd clipCmd) {
		if have(cmd.name) {
			out = append(out, cmd)
		}
	}
	powershell := func() {
		for _, ps := range []string{"powershell.exe", "pwsh"} {
			if have(ps) {
				out = append(out, clipCmd{ps, []string{"-NoProfile", "-NonInteractive", "-STA", "-Command", psScript}, outBase64})
				return
			}
		}
	}
	switch c.GOOS {
	case "darwin":
		// A file copied in Finder: prefer the file over its icon (codex
		// prefers clipboard files too).
		add(clipCmd{"osascript", []string{"-e", "POSIX path of (the clipboard as «class furl»)"}, outPath})
		add(clipCmd{"pngpaste", []string{"-"}, outRaw})
		add(clipCmd{"osascript", []string{"-e", "the clipboard as «class PNGf»"}, outHex})
	case "windows":
		powershell()
	default:
		if c.Getenv("WAYLAND_DISPLAY") != "" {
			add(clipCmd{"wl-paste", []string{"--no-newline", "--type", "image/png"}, outRaw})
		}
		if c.Getenv("DISPLAY") != "" {
			add(clipCmd{"xclip", []string{"-selection", "clipboard", "-target", "image/png", "-out"}, outRaw})
		}
		if c.Getenv("WSL_DISTRO_NAME") != "" || c.Getenv("WSL_INTEROP") != "" {
			// WSL: the image is on the Windows clipboard (codex does the same).
			powershell()
		}
	}
	return out
}

// ReadImage returns the bytes of the clipboard image. Each tool's output is
// offered to accept, which returns an error for data that is not a usable
// image (a tool may hand back text); the first accepted output wins and the
// next tool is tried otherwise. ErrNoImage means none worked.
func (c Clipboard) ReadImage(ctx context.Context, accept func([]byte) error) ([]byte, error) {
	cmds := c.commands()
	if len(cmds) == 0 {
		switch c.GOOS {
		case "darwin", "windows":
			return nil, errors.New("no clipboard tool found")
		}
		return nil, errors.New("no clipboard tool found: install wl-clipboard (Wayland) or xclip (X11)")
	}
	for _, cmd := range cmds {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		data, err := c.fetch(ctx, cmd)
		cancel()
		if err != nil || len(data) == 0 {
			continue
		}
		if accept == nil || accept(data) == nil {
			return data, nil
		}
	}
	return nil, ErrNoImage
}

func (c Clipboard) fetch(ctx context.Context, cmd clipCmd) ([]byte, error) {
	out, err := c.Run(ctx, cmd.name, cmd.args...)
	if err != nil {
		return nil, err
	}
	switch cmd.out {
	case outBase64:
		return base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	case outHex:
		return decodeAppleScriptData(out)
	case outPath:
		path := strings.TrimSpace(string(out))
		if !imageExt(path) {
			return nil, fmt.Errorf("%s is not an image", path)
		}
		return c.ReadFile(path)
	}
	return out, nil
}

// imageExt reports whether a copied file's name looks like an image the
// model can take (the same set as images.PastedPath).
func imageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// decodeAppleScriptData parses osascript's «data PNGf89504E47…» output.
func decodeAppleScriptData(out []byte) ([]byte, error) {
	s := strings.TrimSpace(string(out))
	s = strings.TrimPrefix(s, "«data ")
	s = strings.TrimSuffix(s, "»")
	if len(s) < 4 {
		return nil, ErrNoImage
	}
	return hex.DecodeString(s[4:]) // skip the type code, e.g. "PNGf"
}
