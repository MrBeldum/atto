package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"slices"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// isImage stands in for images.Prepare: it accepts decodable images only.
func isImage(data []byte) error {
	_, _, err := image.DecodeConfig(bytes.NewReader(data))
	return err
}

// fakeClipboard records the commands run and answers from a table.
type fakeClipboard struct {
	tools   map[string]bool
	env     map[string]string
	outputs map[string][]byte // command name -> stdout (absent: fails)
	ran     []string
	stdins  []string // stdin of each RunStdin call
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
		RunStdin: func(_ context.Context, stdin, name string, args ...string) error {
			f.ran = append(f.ran, name+" "+strings.Join(args, " "))
			f.stdins = append(f.stdins, stdin)
			if _, ok := f.outputs[name]; ok {
				return nil
			}
			return errors.New("exit status 1")
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
	im, err := mac.ReadImage(ctx, isImage)
	if err != nil || !bytes.Equal(im, img) || len(f.ran) != 2 {
		t.Fatalf("mac: %v ran %q", err, f.ran)
	}

	// A file copied in Finder wins.
	f = &fakeClipboard{tools: map[string]bool{"osascript": true}, outputs: map[string][]byte{
		"osascript":              []byte("/Users/me/pic.png\n"),
		"file:/Users/me/pic.png": img,
	}}
	if im, err := f.clipboard("darwin").ReadImage(ctx, isImage); err != nil || !bytes.Equal(im, img) || len(f.ran) != 1 {
		t.Fatalf("finder file: %v %v", err, f.ran)
	}

	// Windows: base64 from PowerShell.
	f = &fakeClipboard{tools: map[string]bool{"powershell.exe": true}, outputs: map[string][]byte{
		"powershell.exe": []byte(base64.StdEncoding.EncodeToString(img) + "\r\n"),
	}}
	if im, err := f.clipboard("windows").ReadImage(ctx, isImage); err != nil || !bytes.Equal(im, img) || !strings.Contains(f.ran[0], "-Format Image") {
		t.Fatalf("windows: %v %v", err, f.ran)
	}

	// Text on the clipboard, or nothing installed.
	f = &fakeClipboard{tools: map[string]bool{"xclip": true}, env: map[string]string{"DISPLAY": ":0"}, outputs: map[string][]byte{"xclip": []byte("hello")}}
	if _, err := f.clipboard("linux").ReadImage(ctx, isImage); !errors.Is(err, ErrNoImage) {
		t.Fatalf("text clipboard: %v", err)
	}
	f = &fakeClipboard{}
	if _, err := f.clipboard("linux").ReadImage(ctx, isImage); err == nil || !strings.Contains(err.Error(), "xclip") {
		t.Fatalf("no tools: %v", err)
	}
}

func TestCopyCommand(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			if slices.Contains(names, n) {
				return "/bin/" + n, nil
			}
			return "", errors.New("not found")
		}
	}
	cb := func(goos string, lookPath func(string) (string, error), getenv func(string) string) Clipboard {
		return Clipboard{GOOS: goos, LookPath: lookPath, Getenv: getenv}
	}
	none := env(nil)
	if c := cb("darwin", has(), none).copyCommand(); c[0] != "pbcopy" {
		t.Fatal(c)
	}
	if c := cb("windows", has(), none).copyCommand(); c[0] != "powershell.exe" {
		t.Fatal(c)
	}
	if c := cb("linux", has("wl-copy", "xclip"), env(map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})).copyCommand(); c[0] != "wl-copy" {
		t.Fatal(c)
	}
	if c := cb("linux", has("xclip"), env(map[string]string{"DISPLAY": ":0"})).copyCommand(); c[0] != "xclip" {
		t.Fatal(c)
	}
	if c := cb("linux", has("xsel"), env(map[string]string{"DISPLAY": ":0"})).copyCommand(); c[0] != "xsel" {
		t.Fatal(c)
	}
	if c := cb("linux", has("xclip"), none).copyCommand(); c != nil {
		t.Fatal("no display: no command, OSC 52 only", c)
	}
}

func TestWriteText(t *testing.T) {
	// Windows feeds PowerShell base64 so non-ASCII text survives.
	f := &fakeClipboard{outputs: map[string][]byte{"powershell.exe": nil}}
	if err := f.clipboard("windows").WriteText("héllo"); err != nil {
		t.Fatal(err)
	}
	if want := base64.StdEncoding.EncodeToString([]byte("héllo")); f.stdins[0] != want {
		t.Fatalf("stdin %q want %q", f.stdins[0], want)
	}

	f = &fakeClipboard{outputs: map[string][]byte{"pbcopy": nil}}
	if err := f.clipboard("darwin").WriteText("plain"); err != nil || f.stdins[0] != "plain" || f.ran[0] != "pbcopy " {
		t.Fatalf("darwin: %v %q %q", err, f.stdins, f.ran)
	}

	// No tool: a clear error, nothing run.
	f = &fakeClipboard{}
	if err := f.clipboard("linux").WriteText("x"); !errors.Is(err, ErrNoCopyTool) || len(f.ran) != 0 {
		t.Fatalf("no tool: %v %q", err, f.ran)
	}
}

func TestWritePrimary(t *testing.T) {
	cases := []struct {
		goos  string
		env   map[string]string
		tools []string
		want  string // command run, "" for none
	}{
		{"linux", map[string]string{"WAYLAND_DISPLAY": "w"}, []string{"wl-copy"}, "wl-copy --primary"},
		{"linux", map[string]string{"DISPLAY": ":0"}, []string{"xclip"}, "xclip -selection primary"},
		{"freebsd", map[string]string{"DISPLAY": ":0"}, []string{"xsel"}, "xsel --primary --input"},
		{"linux", nil, []string{"xclip"}, ""},
		{"darwin", map[string]string{"DISPLAY": ":0"}, []string{"xclip"}, ""},
	}
	for _, c := range cases {
		f := &fakeClipboard{env: c.env, tools: map[string]bool{}, outputs: map[string][]byte{}}
		for _, tool := range c.tools {
			f.tools[tool] = true
			f.outputs[tool] = nil
		}
		err := f.clipboard(c.goos).WritePrimary("hi")
		if c.want == "" {
			if err != ErrNoCopyTool || len(f.ran) != 0 {
				t.Errorf("%s %v: err %v ran %q", c.goos, c.env, err, f.ran)
			}
			continue
		}
		if err != nil || !slices.Equal(f.ran, []string{c.want}) || f.stdins[0] != "hi" {
			t.Errorf("%s %v: err %v ran %q", c.goos, c.env, err, f.ran)
		}
	}
}
