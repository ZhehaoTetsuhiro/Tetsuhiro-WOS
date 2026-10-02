package server

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"twos/optics"
)

const testDef = `{
  "name": "metalens",
  "label": "超表面透镜",
  "behavior": "transmit",
  "class": "lens",
  "params": [{"key": "f", "label": "焦距", "unit": "m", "kind": "float", "default": 0.3}],
  "phase": "-k*(sqrt(r*r+f*f) - f)"
}`

// withDefinitions loads one temporary definition directory for the duration of
// a test and clears the registry afterwards.
func withDefinitions(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "metalens.json"), []byte(testDef), 0o644); err != nil {
		t.Fatal(err)
	}
	optics.SetElementDirs([]string{dir})
	rep := optics.ReloadElementDefinitions(nil)
	if len(rep.Errors) != 0 || len(rep.Loaded) != 1 {
		t.Fatalf("reload report = %+v", rep)
	}
	t.Cleanup(func() {
		optics.SetElementDirs([]string{t.TempDir()})
		optics.ReloadElementDefinitions(nil)
	})
	return dir
}

func TestElementEndpoints(t *testing.T) {
	dir := withDefinitions(t)
	srv := New(64 << 20)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// The list names the definition and its parameters.
	res, err := http.Get(ts.URL + "/api/elements")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Dirs     []string           `json:"dirs"`
		Elements []elementListEntry `json:"elements"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(list.Elements) != 1 || list.Elements[0].Name != "metalens" || list.Elements[0].Class != "lens" {
		t.Fatalf("element list = %+v", list)
	}
	if len(list.Elements[0].Params) != 1 || list.Elements[0].Phase == "" {
		t.Fatalf("element list entry lost its parameters or expression: %+v", list.Elements[0])
	}
	if len(list.Dirs) != 1 || list.Dirs[0] != dir {
		t.Fatalf("dirs = %v, want [%s]", list.Dirs, dir)
	}

	// The catalog carries it as a custom element and publishes the class map.
	res, err = http.Get(ts.URL + "/api/catalog")
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Elements []struct {
			Type   string `json:"type"`
			Custom bool   `json:"custom"`
			Source string `json:"source"`
		} `json:"elements"`
		Classes map[string]string `json:"classes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&cat); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	found := false
	for _, d := range cat.Elements {
		if d.Type == "metalens" {
			found = d.Custom && d.Source != ""
		}
	}
	if !found {
		t.Fatalf("catalog does not carry the scripted element as custom: %+v", cat.Elements)
	}
	if cat.Classes["metalens"] != "lens" || cat.Classes["mirror"] != "mirror" {
		t.Fatalf("catalog classes = %v", cat.Classes)
	}

	// Both preview kinds answer with a decodable PNG.
	for _, kind := range []string{"amp", "phase"} {
		res, err = http.Get(ts.URL + "/api/elements/metalens/preview.png?kind=" + kind + "&size=64&f=0.3")
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("preview %s: status %d", kind, res.StatusCode)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(res.Body); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if _, err := png.Decode(&buf); err != nil {
			t.Fatalf("preview %s is not a PNG: %v", kind, err)
		}
	}

	// Unknown element and bad parameters answer with an error, not a crash.
	for _, url := range []string{
		"/api/elements/nope/preview.png",
		"/api/elements/metalens/preview.png?f=abc",
		"/api/elements/metalens/preview.png?kind=hue",
		"/api/elements/metalens/other",
	} {
		res, err := http.Get(ts.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Fatalf("%s answered 200, want an error", url)
		}
	}

	// Reload picks up a file added after startup.
	if err := os.WriteFile(filepath.Join(dir, "vortex.json"),
		[]byte(`{"name": "vortex", "phase": "2*th"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/elements/reload", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var rep optics.DefinitionReport
	if err := json.NewDecoder(res.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(rep.Loaded) != 2 {
		t.Fatalf("reload after adding a file loaded %+v", rep)
	}
	res, err = http.Get(ts.URL + "/api/elements/vortex/preview.png?kind=phase&size=32")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("newly reloaded element preview status %d", res.StatusCode)
	}
	// GET is not a reload.
	res, err = http.Get(ts.URL + "/api/elements/reload")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET reload status = %d, want 405", res.StatusCode)
	}
}

// A broken definition reports per-file errors from the reload endpoint instead
// of taking the previously loaded set down.
func TestElementReloadErrors(t *testing.T) {
	dir := withDefinitions(t)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"name": "broken", "phase": "1 +"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(64 << 20)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/elements/reload", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var rep optics.DefinitionReport
	if err := json.NewDecoder(res.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Source, "broken.json") {
		t.Fatalf("reload errors = %+v", rep.Errors)
	}
	if len(rep.Loaded) != 1 || rep.Loaded[0].Name != "metalens" {
		t.Fatalf("a broken file must not remove the good ones: %+v", rep.Loaded)
	}
}
