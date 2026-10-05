package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/extensions"
)

// atto -p runs extensions without a UI: dialogs get their defaults,
// notices go to stderr, and the events fire.
func TestRunPrintExtensions(t *testing.T) {
	bodies := imageModelServer(t, `["text"]`)
	cwd := t.TempDir()
	t.Chdir(cwd)
	ext := filepath.Join(os.Getenv("ATTO_DIR"), "extensions", "ctx.ts")
	if err := os.MkdirAll(filepath.Dir(ext), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `export default function (atto: any) {
  atto.on("session_start", (e: any) => atto.fs.writeFile("log.txt", e.reason));
  atto.on("session_end", (e: any) => atto.fs.writeFile("log.txt", atto.fs.readFile("log.txt") + "," + e.reason));
  atto.on("user_prompt", async (_e: any, ctx: any) => {
    ctx.ui.notify("asked: " + await ctx.ui.confirm("ok?") + " " + ctx.hasUI);
    return "Extra context from an extension.";
  });
}`
	if err := os.WriteFile(ext, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	quiet(t)
	errOut, err := os.CreateTemp(t.TempDir(), "err")
	if err != nil {
		t.Fatal(err)
	}
	defer errOut.Close()
	os.Stderr = errOut
	if err := RunPrint(PrintOptions{Prompt: "hi", NoSave: true}); err != nil {
		t.Fatal(err)
	}
	if b := bodies(); len(b) != 1 || !strings.Contains(b[0], `hi\n\nExtra context from an extension.`) {
		t.Fatalf("request: %v", b)
	}
	if data, _ := os.ReadFile(errOut.Name()); !strings.Contains(string(data), "[ctx] asked: false false\n") {
		t.Fatalf("stderr %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(cwd, "log.txt")); string(data) != "startup,other" {
		t.Fatalf("events %q", data)
	}
}

func TestExtensionsCLI(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("ATTO_DIR", filepath.Join(root, "atto"))
	t.Setenv(config.EnvAgent, "")
	cwd := filepath.Join(root, "proj")
	for path, text := range map[string]string{
		filepath.Join(cwd, ".git", "HEAD"):                            "x",
		filepath.Join(cwd, ".atto", "extensions", "local.ts"):         "export default () => {}",
		filepath.Join(root, "atto", "extensions", "mine", "index.ts"): "export default () => {}",
		filepath.Join(root, "atto", "extensions", "bad.js"):           "export default (",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)
	run := func(args ...string) (string, error) {
		var out strings.Builder
		err := RunExtensions(args, &out)
		return out.String(), err
	}
	out, err := run()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bad  failed: ", "bad.js:1:", "mine  ok · user", "local  needs approval · project", "atto extensions approve local"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}

	t.Setenv(config.EnvAgent, "1")
	if _, err := run("approve", "local"); err == nil || !strings.Contains(err.Error(), "approved by the user") {
		t.Fatalf("an agent cannot approve: %v", err)
	}
	t.Setenv(config.EnvAgent, "")
	if out, err := run("approve", "local"); err != nil || !strings.Contains(out, "Approved local") {
		t.Fatal(out, err)
	}
	if out, _ := run("list", "-json"); !strings.Contains(out, `"status": "ready"`) || strings.Contains(out, extensions.NeedsApproval) {
		t.Fatalf("approved:\n%s", out)
	}
	if out, _ := run("types"); out != extensions.Types {
		t.Fatal("types prints atto.d.ts")
	}
	if _, err := run("nope"); err == nil {
		t.Fatal("usage")
	}

	var ctx strings.Builder
	if err := RunContext(nil, &ctx); err != nil || !strings.Contains(ctx.String(), "mine") || !strings.Contains(ctx.String(), "ready · user") {
		t.Fatalf("atto context lists them: %v\n%s", err, ctx.String())
	}
}
