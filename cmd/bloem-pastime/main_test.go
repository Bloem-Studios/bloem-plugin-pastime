package main

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestManifestIsOnePlainWatchSyncCapability(t *testing.T) {
	m, err := manifest.Load(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if m.PluginId != "bloem.pastime" || m.Version != version || m.Metadata != nil {
		t.Fatalf("identity %s %s (binary %s)", m.PluginId, m.Version, version)
	}
	// The plugin and capability IDs form the provider key
	// plugin:<installation>:pastime that existing connections are stored under.
	if len(m.Capabilities) != 1 || m.Capabilities[0].Type != "watch_sync_provider.v1" || m.Capabilities[0].Id != "pastime" {
		t.Fatalf("capabilities %v", m.Capabilities)
	}
	c := m.Capabilities[0]
	if c.Metadata != nil || c.ConfigSchema != nil {
		t.Fatal("no managed metadata and no per-connection settings")
	}
	d := c.WatchSyncProvider
	if len(d.AuthMethods) != 1 || d.AuthMethods[0] != v1.WatchSyncAuthMethod_WATCH_SYNC_AUTH_METHOD_API_KEY || d.MaxBatchSize != 100 {
		t.Fatalf("descriptor %v", d)
	}
	if !d.ScrobblePlayback || !d.ExportWatched || d.ExportUnwatched || d.SyncDropped || d.ProvidesWatchlistOrder {
		t.Fatal("unsupported capability advertised")
	}
	// Plays and resume points Pastime recorded outside Silo come back in.
	if !d.ImportWatched || !d.ImportProgress {
		t.Fatal("watched and progress import missing")
	}
	if !d.ImportFavorites || !d.ExportFavorites || !d.RemoveFavorites || !d.ImportWatchlist || !d.ExportWatchlist || !d.RemoveWatchlist || !d.ImportRatings || !d.ExportRatings {
		t.Fatal("personal-state capabilities missing")
	}
	if len(m.GlobalConfigSchema) != 1 {
		t.Fatalf("global config %v", m.GlobalConfigSchema)
	}
	g := m.GlobalConfigSchema[0]
	var schema struct {
		Properties map[string]struct {
			Type      string `json:"type"`
			MaxLength int    `json:"maxLength"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}
	if err := json.Unmarshal([]byte(g.JsonSchema), &schema); err != nil {
		t.Fatal(err)
	}
	if g.Key != "pastime" || !g.Required || len(schema.Properties) != 1 || schema.Properties["pastime_url"].Type != "string" || len(schema.Required) != 1 || schema.AdditionalProperties {
		t.Fatalf("schema %v", g)
	}
	f := g.AdminForm.Fields
	if len(f) != 1 || f[0].Key != "pastime_url" || f[0].Secret || f[0].Control != v1.AdminFormControl_ADMIN_FORM_CONTROL_TEXT || !f[0].Required || f[0].Validation.GetPattern() != `^https://[^/?#@\s]+/?$` {
		t.Fatalf("form %v", f)
	}
}

// Configure must accept whatever Silo saved, including nothing: a failed
// Configure makes Silo abandon the plugin start.
func TestConfigureAcceptsAnySettings(t *testing.T) {
	fn := configure(hclog.NewNullLogger())
	value := func(url string) *structpb.Struct {
		s, _ := structpb.NewStruct(map[string]any{"pastime_url": url})
		return s
	}
	for _, entries := range [][]*v1.ConfigEntry{
		nil,
		{{Key: "pastime", Value: value("https://pastime.example.com")}},
		{{Key: "pastime", Value: value("http://pastime.example.com")}},
		{{Key: "pastime"}},
		{{Key: "other", Value: value("x")}},
	} {
		if err := fn(context.Background(), entries); err != nil {
			t.Fatalf("%v: %v", entries, err)
		}
	}
}
