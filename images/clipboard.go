package images

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/provider"
)

// Clipboard reads images from the system clipboard. Releases are built
// without cgo, so instead of a native clipboard library (codex uses
// arboard) it shells out to the platform's tools. Every dependency is a
// field so tests can check which commands run without a real clipboard.
type Clipboard struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Run runs a command and returns its stdout.
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)
	ReadFile func(string) ([]byte, error)
}

// SystemClipboard uses the real OS.
func SystemClipboard() Clipboard {
	return Clipboard{
		GOOS:     runtime.GOOS,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		ReadFile: os.ReadFile,
	}
}

// ErrNoImage means the clipboard holds no image.
var ErrNoImage = errors.New("no image on the clipboard")

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

// Read returns the clipboard image, prepared for the model.
func (c Clipboard) Read(ctx context.Context) (provider.Image, error) {
	cmds := c.commands()
	if len(cmds) == 0 {
		switch c.GOOS {
		case "darwin", "windows":
			return provider.Image{}, errors.New("no clipboard tool found")
		}
		return provider.Image{}, errors.New("no clipboard tool found: install wl-clipboard (Wayland) or xclip (X11)")
	}
	for _, cmd := range cmds {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		data, err := c.fetch(ctx, cmd)
		cancel()
		if err != nil || len(data) == 0 {
			continue
		}
		if im, err := Prepare(data); err == nil {
			return im, nil
		}
	}
	return provider.Image{}, ErrNoImage
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
