package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

// fakeClipboard records the commands run and answers from a table.
type fakeClipboard struct {
	tools   map[string]bool
	env     map[string]string
	outputs map[string][]byte // command name -> stdout (absent: fails)
	ran     []string
}

func (f *fakeClipboard) clipboard(goos string) Clipboard {
	return Clipboard{
		GOOS:   goos,
		Getenv: func(k string) string { return f.env[k] },
		LookPath: func(name string) (string, error) {
			if f.tools[name] {
				return "/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			f.ran = append(f.ran, name+" "+strings.Join(args, " "))
			if out, ok := f.outputs[name]; ok {
				return out, nil
			}
			return nil, errors.New("exit status 1")
		},
		ReadFile: func(p string) ([]byte, error) {
			if out, ok := f.outputs["file:"+p]; ok {
				return out, nil
			}
			return nil, os.ErrNotExist
		},
	}
}

func names(cmds []clipCmd) string {
	var out []string
	for _, c := range cmds {
		out = append(out, c.name)
	}
	return strings.Join(out, ",")
}

func TestClipboardCommandSelection(t *testing.T) {
	all := map[string]bool{"osascript": true, "pngpaste": true, "wl-paste": true, "xclip": true, "powershell.exe": true, "pwsh": true}
	for _, tc := range []struct {
		goos  string
		tools map[string]bool
		env   map[string]string
		want  string
	}{
		{"darwin", all, nil, "osascript,pngpaste,osascript"},
		{"darwin", map[string]bool{"osascript": true}, nil, "osascript,osascript"},
		{"linux", all, map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, "wl-paste,xclip"},
		{"linux", all, map[string]string{"DISPLAY": ":0"}, "xclip"},
		{"linux", map[string]bool{"wl-paste": true}, map[string]string{"DISPLAY": ":0"}, ""},
		{"linux", all, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, "powershell.exe"},
		{"windows", all, nil, "powershell.exe"},
		{"windows", map[string]bool{"pwsh": true}, nil, "pwsh"},
	} {
		f := &fakeClipboard{tools: tc.tools, env: tc.env}
		if got := names(f.clipboard(tc.goos).commands()); got != tc.want {
			t.Errorf("%s %v %v: got %q want %q", tc.goos, tc.tools, tc.env, got, tc.want)
		}
	}
}

func TestClipboardRead(t *testing.T) {
	img := pngBytes(t, 4, 2)
	ctx := context.Background()

	// macOS without pngpaste: the furl query fails (no file copied), the
	// PNGf literal is decoded.
	f := &fakeClipboard{tools: map[string]bool{"osascript": true}}
	f.outputs = map[string][]byte{}
	mac := f.clipboard("darwin")
	mac.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		f.ran = append(f.ran, args[1])
		if strings.Contains(args[1], "PNGf") {
			return []byte("«data PNGf" + strings.ToUpper(hex.EncodeToString(img)) + "»\n"), nil
		}
		return nil, errors.New("can't make into type file")
	}
	im, err := mac.Read(ctx)
	if err != nil || !bytes.Equal(im.Data, img) || len(f.ran) != 2 {
		t.Fatalf("mac: %v ran %q", err, f.ran)
	}

	// A file copied in Finder wins.
	f = &fakeClipboard{tools: map[string]bool{"osascript": true}, outputs: map[string][]byte{
		"osascript":              []byte("/Users/me/pic.png\n"),
		"file:/Users/me/pic.png": img,
	}}
	if im, err := f.clipboard("darwin").Read(ctx); err != nil || im.Width != 4 || len(f.ran) != 1 {
		t.Fatalf("finder file: %v %v", err, f.ran)
	}

	// Windows: base64 from PowerShell.
	f = &fakeClipboard{tools: map[string]bool{"powershell.exe": true}, outputs: map[string][]byte{
		"powershell.exe": []byte(base64.StdEncoding.EncodeToString(img) + "\r\n"),
	}}
	if im, err := f.clipboard("windows").Read(ctx); err != nil || im.Height != 2 || !strings.Contains(f.ran[0], "-Format Image") {
		t.Fatalf("windows: %v %v", err, f.ran)
	}

	// Text on the clipboard, or nothing installed.
	f = &fakeClipboard{tools: map[string]bool{"xclip": true}, env: map[string]string{"DISPLAY": ":0"}, outputs: map[string][]byte{"xclip": []byte("hello")}}
	if _, err := f.clipboard("linux").Read(ctx); !errors.Is(err, ErrNoImage) {
		t.Fatalf("text clipboard: %v", err)
	}
	f = &fakeClipboard{}
	if _, err := f.clipboard("linux").Read(ctx); err == nil || !strings.Contains(err.Error(), "xclip") {
		t.Fatalf("no tools: %v", err)
	}
}
