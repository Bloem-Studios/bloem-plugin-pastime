// Command bloem-pastime is the Pastime watch-sync plugin: an ordinary
// watch_sync_provider.v1 capability that sends what connected profiles watch
// to Pastime. It serves only Silo's calls; it runs no background work and
// keeps no state.
package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/config"
	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/provider"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/manifest"
	sdkruntime "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/encoding/protojson"
)

//go:embed manifest.json
var manifestJSON []byte

// version is stamped by the Makefile (-X main.version) and must match
// manifest.json.
var version = "0.4.2"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "manifest" {
		os.Exit(printManifest())
	}
	logger := hclog.New(&hclog.LoggerOptions{Name: "bloem-pastime", Output: os.Stderr, Level: hclog.Info, JSONFormat: true})
	// The SDK's default Runtime answers GetManifest, accepts BindHostBroker
	// without using it, and hands Configure entries to the callback.
	sdkruntime.ServeManifestWithOptions(manifestJSON, version,
		sdkruntime.CapabilityServers{WatchSyncProvider: provider.New(client.New(version))},
		sdkruntime.WithConfigure(configure(logger)))
}

// printManifest writes the checksum-stamped manifest Silo installs from.
func printManifest() int {
	m, err := manifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid embedded Pastime manifest.")
		return 1
	}
	b, err := protojson.Marshal(m)
	if err != nil {
		return 1
	}
	fmt.Println(string(b))
	return 0
}

// configure accepts any saved settings: every watch-sync call carries the
// Pastime address, so nothing is stored here. It only warns once per start
// when the address cannot work, without logging it.
func configure(logger hclog.Logger) sdkruntime.ConfigureFunc {
	return func(_ context.Context, entries []*v1.ConfigEntry) error {
		raw := ""
		for _, entry := range entries {
			if entry.GetKey() == config.Key {
				raw = entry.GetValue().GetFields()[config.Field].GetStringValue()
			}
		}
		_, err := config.Parse(raw, os.Getenv(config.LoopbackEnv) == "1")
		switch {
		case errors.Is(err, config.ErrMissing):
			logger.Warn("The Pastime address is not set; set it in the Pastime plugin settings.")
		case err != nil:
			logger.Warn("The Pastime address is not an https address without a path; Silo cannot reach Pastime until it is fixed.")
		}
		return nil
	}
}
