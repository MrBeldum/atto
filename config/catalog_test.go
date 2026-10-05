package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `{"opencode-go":{"models":{
 "glm-5.3":{"id":"glm-5.3","name":"GLM-5.3","tool_call":true,"reasoning":true,"limit":{"context":1000000,"output":131072}},
 "kimi-k2.6":{"id":"kimi-k2.6","name":"Kimi K2.6","tool_call":true,"reasoning":true,"limit":{"context":262144,"output":65536}},
 "qwen3.8-flash":{"id":"qwen3.8-flash","tool_call":true,"reasoning":true,"limit":{"context":1000000,"output":131072},"provider":{"npm":"@ai-sdk/anthropic"}},
 "old":{"id":"old","tool_call":true,"status":"deprecated","limit":{"context":1000,"output":100}},
 "notools":{"id":"notools","tool_call":false,"limit":{"context":1000,"output":100}}
}}}`

func TestCatalogAndMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATTO_DIR", dir)
	t.Setenv("OPENCODE_API_KEY", "")
	os.MkdirAll(filepath.Join(dir, "cache"), 0o755)
	os.WriteFile(filepath.Join(dir, "cache", "catalog.json"), []byte(fixture), 0o644)

	// No key: preset hidden.
	m, err := LoadModels()
	if err != nil || len(m.Providers) != 0 {
		t.Fatalf("providers without key: %v %v", m.Providers, err)
	}

	if err := SetAPIKey("opencode-go", "k-123"); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(AuthPath()); st.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json mode %v", st.Mode())
	}
	os.WriteFile(ModelsPath(), []byte(`{"providers":{"opencode-go":{"models":[{"id":"glm-5.3","name":"My GLM","efforts":["high"]}]}}}`), 0o644)
	m, err = LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	refs := m.List()
	if len(refs) != 2 {
		t.Fatalf("want glm-5.3 and kimi-k2.6, got %+v", refs)
	}
	glm, ok := m.Find("", "opencode-go/glm-5.3")
	if !ok || glm.Model.Name != "My GLM" || glm.APIKey != "k-123" || glm.Provider.Headers["x-opencode-session"] != "$session" {
		t.Fatalf("override/key/header: %+v", glm)
	}
	kimi, _ := m.Find("opencode-go", "kimi-k2.6")
	body := kimi.RequestBody()
	if _, has := body["reasoning_effort"]; has || body["thinking"] == nil || len(kimi.Model.Levels()) != 2 {
		t.Fatalf("kimi quirk: %+v %v", kimi.Model, body)
	}
}

func TestCatalogEffortOff(t *testing.T) {
	m, _ := catalogModel("opencode-go", "deepseek-v4.1-flash", modelsDevModel{ToolCall: true, Reasoning: true})
	if *m.EffortMap["off"] != "none" || m.Levels()[0] != "off" {
		t.Fatalf("deepseek off: %+v", m)
	}
	g, _ := catalogModel("opencode-go", "glm-5.3", modelsDevModel{ToolCall: true, Reasoning: true})
	for _, e := range g.Levels() {
		if e == "off" {
			t.Fatalf("glm-5.3 cannot disable thinking: %v", g.Efforts)
		}
	}
}

func TestEffortMapLikePi(t *testing.T) {
	ds, _ := catalogModel("opencode-go", "deepseek-v4-pro", modelsDevModel{ToolCall: true, Reasoning: true})
	if got := ds.Levels(); strings.Join(got, ",") != "off,high,max" {
		t.Fatalf("deepseek-v4-pro levels %v", got)
	}
	fl, _ := catalogModel("opencode-go", "glm-5.3-flash", modelsDevModel{ToolCall: true, Reasoning: true})
	if fl.Levels()[0] != "off" {
		t.Fatalf("glm-5.3-flash should keep off: %v", fl.Levels())
	}
	// A user override changes one level and keeps the rest.
	user := Model{ID: "deepseek-v4-pro", EffortMap: map[string]*string{"off": nil, "xhigh": str("max")}}
	m := mergeModel(ds, user)
	if got := strings.Join(m.Levels(), ","); got != "high,xhigh,max" {
		t.Fatalf("merged levels %s", got)
	}
	if m.WireEfforts()["xhigh"] != "max" {
		t.Fatalf("wire %v", m.WireEfforts())
	}
}
