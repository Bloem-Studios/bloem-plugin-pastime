package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// fakeRelease has one non-native platform, so no binary needs to run.
func fakeRelease(t *testing.T) *release {
	t.Helper()
	binary := []byte("synthetic plugin binary")
	r := &release{
		dir: t.TempDir(), version: "0.2.0", platforms: []string{"plan9/amd64"}, native: "none/none",
		manifest: &v1.PluginManifest{PluginId: "bloem.pastime", Version: "0.2.0", Checksum: "native", SupportedPlatforms: []*v1.SupportedPlatform{{Os: "plan9", Arch: "amd64"}},
			GlobalConfigSchema: []*v1.ConfigSchema{{Key: "pastime", Required: true, AdminForm: &v1.AdminFormDescriptor{Fields: []*v1.AdminFormField{{Key: "pastime_url", Control: v1.AdminFormControl_ADMIN_FORM_CONTROL_TEXT}}}}}},
		binaries:  map[string][]byte{"plan9/amd64": binary},
		checksums: map[string]string{"plan9/amd64": sum(binary)},
	}
	if err := r.pack(); err != nil {
		t.Fatal(err)
	}
	return r
}

func rewriteArchive(t *testing.T, r *release, change func(name string, data []byte) []byte) {
	t.Helper()
	path := filepath.Join(r.dir, archiveName(r.version, "plan9/amd64"))
	files, err := readZip(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range []string{"manifest.json", "plugin"} {
		data := change(name, files[name])
		f, _ := w.Create(name)
		_, _ = f.Write(data)
		if name == "manifest.json" {
			// Keep the loose copy in step so only the content checks can fail.
			if err := os.WriteFile(filepath.Join(r.dir, "manifest-plan9-amd64.json"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = w.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPackedArchivesPassAndTamperingFails(t *testing.T) {
	r := fakeRelease(t)
	if err := r.checkArchives(); err != nil {
		t.Fatalf("packed release: %v", err)
	}
	files, _ := readZip(filepath.Join(r.dir, archiveName(r.version, "plan9/amd64")))
	m := &v1.PluginManifest{}
	if protojson.Unmarshal(files["manifest.json"], m) != nil || m.Checksum != sum(files["plugin"]) {
		t.Fatal("archive manifest checksum must be the plugin's sha256")
	}
	editManifest := func(edit func(*v1.PluginManifest)) func(string, []byte) []byte {
		return func(n string, b []byte) []byte {
			if n != "manifest.json" {
				return b
			}
			m := &v1.PluginManifest{}
			if err := protojson.Unmarshal(b, m); err != nil {
				t.Fatal(err)
			}
			edit(m)
			out, _ := protojson.Marshal(m)
			return out
		}
	}
	for name, change := range map[string]func(string, []byte) []byte{
		// The H7 incident: a manifest declaring other platforms than the binary.
		"platforms": editManifest(func(m *v1.PluginManifest) {
			m.SupportedPlatforms = []*v1.SupportedPlatform{{Os: "linux", Arch: "amd64"}}
		}),
		"version": editManifest(func(m *v1.PluginManifest) { m.Version = "0.1.14" }),
		"binary": func(n string, b []byte) []byte {
			if n == "plugin" {
				return append(b, '!')
			}
			return b
		},
	} {
		r := fakeRelease(t)
		rewriteArchive(t, r, change)
		if err := r.checkArchives(); err == nil {
			t.Errorf("%s: tampered archive passed", name)
		}
	}
}

func TestCatalogEntryMustMatch(t *testing.T) {
	r := fakeRelease(t)
	write := func(body string) string {
		path := filepath.Join(r.dir, "catalog.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// The packed entry is what gets published: Silo's wire format.
	entry, err := os.ReadFile(filepath.Join(r.dir, catalogEntryFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(entry, []byte(`"control": 1`)) || !bytes.Contains(entry, []byte(`"supported_platforms"`)) || !bytes.Contains(entry, []byte(`/pastime-v0.2.0/plugin-plan9-amd64`)) {
		t.Fatalf("catalog entry is not in Silo's format:\n%s", entry)
	}
	if err := r.checkCatalogEntry(); err != nil {
		t.Fatal(err)
	}
	good := `{"plugins":[` + string(entry) + `]}`
	if err := r.checkCatalog(write(good)); err != nil {
		t.Fatal(err)
	}
	compact := func(s string) string {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	good = compact(good)
	for name, bad := range map[string]string{
		"checksum": strings.Replace(good, `"checksum":"`+r.checksums["plan9/amd64"], `"checksum":"`+strings.Repeat("0", 64), 1),
		"field":    strings.Replace(good, `"supported_platforms"`, `"silo_api_version":"v1","supported_platforms"`, 1),
		"platform": strings.Replace(good, `"plan9/amd64":`, `"linux/amd64":`, 1),
		"version":  strings.Replace(good, `"version":"0.2.0"`, `"version":"0.1.14"`, 1),
		"no URL":   strings.Replace(good, `"url":"https://github.com/Bloem-Studios/bloem-plugins/releases/download/pastime-v0.2.0/plugin-plan9-amd64"`, `"url":""`, 1),
		// protojson accepts these; Silo's encoding/json refuses the whole
		// catalog for an enum name and silently drops a camelCase key.
		"enum name":                      strings.Replace(good, `"control":1`, `"control":"ADMIN_FORM_CONTROL_TEXT"`, 1),
		"camelCase":                      strings.Replace(good, `"supported_platforms"`, `"supportedPlatforms"`, 1),
		"other entry Silo cannot decode": strings.Replace(good, `{"plugins":[`, `{"plugins":[{"manifest":{"plugin_id":"other","supported_platforms":"x"}},`, 1),
	} {
		if bad == good {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if err := r.checkCatalog(write(bad)); err == nil {
			t.Errorf("%s: accepted %s", name, bad)
		}
	}
}
