package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const processKey = "ptbs_process_synthetic"

// processPastime is the smallest Pastime the process test needs.
type processPastime struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
}

func (p *processPastime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.paths = append(p.paths, r.URL.Path)
	p.bodies = append(p.bodies, string(body))
	p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+processKey {
		w.WriteHeader(401)
		_, _ = fmt.Fprint(w, `{"code":"unauthenticated","message":"x"}`)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/api/v1/integrations/bloem") {
	case "/credentials:inspect":
		_, _ = fmt.Fprint(w, `{"instance_id":"instance-1","account_id":"7","profile_id":"3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71","name":"Robin"}`)
	case "/events:apply":
		_, _ = fmt.Fprint(w, `{"status":"applied","play_recorded":true}`)
	case "/state:read":
		_, _ = fmt.Fprint(w, `{"items":[{"provider_item_key":"catalog:9","kind":"favorite","media_kind":"movie","external_ids":{"tmdb":"603"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"},{"provider_item_key":"catalog:8","kind":"favorite","media_kind":"movie","external_ids":{"tmdb":"604"},"removed":true,"changed_at":"2026-10-01T10:00:00Z"}],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`)
	default:
		w.WriteHeader(404)
	}
}

// broker answers the host side of BindHostBroker, as Silo does, with a
// RuntimeHost that implements nothing: the plugin must not need it.
type runtimeHost struct {
	v1.UnimplementedRuntimeHostServer
}

// TestRealProcessStartsLikeSiloStartsIt runs the built binary through the
// same steps as Silo's plugin host: read the manifest, start the process,
// bind the host broker, compare the live manifest with the installed one,
// configure it, then call the watch-sync capability.
func TestRealProcessStartsLikeSiloStartsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the plugin binary")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "plugin")
	build := exec.Command("go", "build", "-race", "-ldflags", "-X main.version="+version, "-o", binary, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}

	// Silo installs from the binary's own manifest output.
	printed, err := exec.Command(binary, "manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	installed := &v1.PluginManifest{}
	if err := protojson.Unmarshal(printed, installed); err != nil {
		t.Fatal(err)
	}
	binaryBytes, _ := os.ReadFile(binary)
	sum := sha256.Sum256(binaryBytes)
	if installed.Checksum != hex.EncodeToString(sum[:]) || installed.Version != version {
		t.Fatalf("manifest checksum %s version %s", installed.Checksum, installed.Version)
	}

	pastime := &processPastime{}
	srv := httptest.NewServer(pastime)
	defer srv.Close()

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "BLOEM_PASTIME_ALLOW_LOOPBACK_HTTP=1")
	logs := &processLogs{}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  sdkruntime.HandshakeConfig(),
		Plugins:          sdkruntime.DefaultPluginSet(sdkruntime.CapabilityServers{}),
		Cmd:              cmd,
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger:           hclog.New(&hclog.LoggerOptions{Output: io.Discard}),
		Stderr:           logs,
		StartTimeout:     10 * time.Second,
	})
	defer client.Kill()
	protocol, err := client.Client()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := protocol.Dispense(sdkruntime.PluginSetName)
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*sdkruntime.Client)
	// Silo bounds every watch-sync call with two minutes
	// (pluginhost.DefaultWatchSyncTimeout); gRPC carries the deadline to the
	// plugin, which keeps 25 seconds in reserve for each Pastime request.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	id := c.Broker().NextId()
	go c.Broker().AcceptAndServe(id, func(opts []grpc.ServerOption) *grpc.Server {
		g := grpc.NewServer(opts...)
		v1.RegisterRuntimeHostServer(g, runtimeHost{})
		return g
	})
	if _, err := c.Runtime().BindHostBroker(ctx, &v1.BindHostBrokerRequest{BrokerId: id}); err != nil {
		t.Fatalf("bind host broker: %v", err)
	}
	live, err := c.Runtime().GetManifest(ctx, &v1.GetManifestRequest{})
	if err != nil || !proto.Equal(live.GetManifest(), installed) {
		t.Fatalf("live manifest differs from the installed one: %v", err)
	}
	settings, _ := structpb.NewStruct(map[string]any{"pastime_url": srv.URL})
	if _, err := c.Runtime().Configure(ctx, &v1.ConfigureRequest{Config: []*v1.ConfigEntry{{Key: "pastime", Value: settings}}}); err != nil {
		t.Fatalf("configure: %v", err)
	}

	providerConfig := &v1.WatchSyncProviderConfig{Values: map[string]string{"pastime.pastime_url": srv.URL}}
	watchSync := c.WatchSyncProvider()
	connected, err := watchSync.ExchangeAPIKey(ctx, &v1.WatchSyncExchangeAPIKeyRequest{CapabilityId: "pastime", ProviderConfig: providerConfig, ApiKey: processKey})
	if err != nil || connected.Fault != nil || connected.Credentials.GetExpiresAt() != nil || connected.Credentials.GetRefreshToken() != "" || connected.Account.GetUsername() != "Robin" {
		t.Fatalf("exchange: %v %v", connected, err)
	}
	auth := &v1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", ProviderConfig: providerConfig, Credentials: connected.Credentials}
	event := &v1.WatchSyncEvent{EventId: "history-1", Operation: v1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED, OccurredAt: timestamppb.New(time.Now().Add(-time.Hour)), WatchHistoryId: "0b5e7c1a-9d2f-4e6b-8a3c-5f1d2e3c4b5a", DurationSeconds: 6000, Media: &v1.WatchSyncMedia{MediaItemId: "c0ffee00-0000-4000-8000-000000000001", MediaType: v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": "603"}}}
	applied, err := watchSync.ApplyEvents(ctx, &v1.WatchSyncApplyEventsRequest{Context: auth, Events: []*v1.WatchSyncEvent{event}})
	if err != nil || applied.Fault != nil || len(applied.Results) != 1 || applied.Results[0].Status != v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED || applied.UpdatedCredentials != nil {
		t.Fatalf("apply: %v %v", applied, err)
	}
	// A call whose deadline leaves no room for a request sends nothing and
	// hands the event back for the next run: the deadline reached the plugin.
	short, cancelShort := context.WithTimeout(ctx, 20*time.Second)
	deferred, err := watchSync.ApplyEvents(short, &v1.WatchSyncApplyEventsRequest{Context: auth, Events: []*v1.WatchSyncEvent{event}})
	cancelShort()
	if err != nil || deferred.Fault != nil || len(deferred.Results) != 1 || deferred.Results[0].Status != v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY || deferred.Results[0].Fault.GetCode() != v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_RATE_LIMITED {
		t.Fatalf("short deadline: %v %v", deferred, err)
	}
	state, err := watchSync.ListRemoteState(ctx, &v1.WatchSyncListRemoteStateRequest{Context: auth, PageSize: 100, StateKinds: []v1.WatchSyncRemoteStateKind{v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE}})
	// The favourite and the removal Pastime names (catalog:8) both arrive.
	if err != nil || state.Fault != nil || state.CompleteSnapshot || len(state.Items) != 2 || !state.Items[1].GetFavorite().GetRemoved() || state.NextCursor != "full-v1" {
		t.Fatalf("remote state: %v %v", state, err)
	}

	pastime.mu.Lock()
	sent := strings.Join(pastime.bodies, "\n")
	pastime.mu.Unlock()
	if !strings.Contains(sent, `"consumption_id":"play_d5d9186f6e2ab5b22309a289e2c222c15c090ac946666d33e8bc7b0b42a6bad5"`) {
		t.Fatalf("the export did not carry the legacy consumption ID: %s", sent)
	}
	client.Kill()
	if logs.contains("DATA RACE") || logs.contains(processKey) {
		t.Fatal("the plugin raced or logged the key")
	}
}

type processLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *processLogs) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *processLogs) contains(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Contains(b.buf.String(), s)
}
