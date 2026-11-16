package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/config"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testKey = "ptbs_synthetic"

// testNow is after every occurrence the tests use.
var testNow = time.Date(2026, 10, 14, 13, 0, 0, 123456789, time.UTC)

type request struct {
	path string
	body []byte
}

// answer is one canned Pastime reply.
type answer struct {
	status int
	header string // Retry-After
	body   string
}

// fakePastime records requests and answers them in order per path, falling
// back to a success.
type fakePastime struct {
	t        *testing.T
	mu       sync.Mutex
	binding  client.Binding
	requests []request
	answers  map[string][]answer
	// delay slows every event write, to run batches against a deadline.
	delay time.Duration
	srv   *httptest.Server
}

func newFake(t *testing.T) *fakePastime {
	t.Helper()
	t.Setenv(config.LoopbackEnv, "1")
	f := &fakePastime{t: t, binding: client.Binding{InstanceID: "instance-1", AccountID: goldenAccount, ProfileID: goldenProfile, Name: "Robin"}, answers: map[string][]answer{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePastime) queue(path string, answers ...answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[path] = append(f.answers[path], answers...)
}

func (f *fakePastime) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/integrations/bloem")
	f.mu.Lock()
	delay := f.delay
	f.mu.Unlock()
	if delay > 0 && strings.HasSuffix(path, "events:apply") {
		time.Sleep(delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.requests = append(f.requests, request{path: path, body: body})
	if r.Header.Get("Authorization") != "Bearer "+testKey {
		w.WriteHeader(401)
		_, _ = fmt.Fprint(w, `{"code":"unauthenticated","message":"x"}`)
		return
	}
	if queued := f.answers[path]; len(queued) > 0 {
		a := queued[0]
		f.answers[path] = queued[1:]
		if a.header != "" {
			w.Header().Set("Retry-After", a.header)
		}
		w.WriteHeader(a.status)
		_, _ = fmt.Fprint(w, a.body)
		return
	}
	switch path {
	case "/credentials:inspect":
		_ = json.NewEncoder(w).Encode(f.binding)
	case "/events:apply":
		var e client.PlaybackEvent
		if err := json.Unmarshal(body, &e); err != nil {
			f.t.Errorf("playback body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(client.EventResult{Status: "applied", PlayRecorded: e.Completed})
	case "/state/events:apply":
		_, _ = fmt.Fprint(w, `{"status":"applied","play_recorded":false}`)
	case "/state:read":
		_, _ = fmt.Fprint(w, `{"items":[],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`)
	default:
		w.WriteHeader(404)
	}
}

// sent returns the bodies posted to path.
func (f *fakePastime) sent(path string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]byte
	for _, r := range f.requests {
		if r.path == path {
			out = append(out, r.body)
		}
	}
	return out
}

// bindingAnswer is a successful credentials:inspect reply.
func (f *fakePastime) bindingAnswer() answer {
	b, err := json.Marshal(f.binding)
	if err != nil {
		f.t.Fatal(err)
	}
	return answer{200, "", string(b)}
}

func (f *fakePastime) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakePastime) provider() *Provider {
	p := New(client.New("test"))
	p.now = func() time.Time { return testNow }
	return p
}

func (f *fakePastime) config() *v1.WatchSyncProviderConfig {
	return &v1.WatchSyncProviderConfig{Values: map[string]string{config.ValueKey: f.srv.URL}}
}

// auth is a connection as Silo stores it, legacy or new: the key plus the
// three binding attributes.
func (f *fakePastime) auth() *v1.WatchSyncAuthenticatedContext {
	return &v1.WatchSyncAuthenticatedContext{
		CapabilityId:   "pastime",
		ProviderConfig: f.config(),
		Credentials: &v1.WatchSyncCredentials{AccessToken: testKey, SecretAttributes: map[string]string{
			"instance_id": f.binding.InstanceID, "account_id": f.binding.AccountID, "profile_id": f.binding.ProfileID,
		}},
	}
}

func at(s string) *timestamppb.Timestamp {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return timestamppb.New(t)
}

func movie(id string) *v1.WatchSyncMedia {
	return &v1.WatchSyncMedia{MediaItemId: id, MediaType: v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": "603", "imdb": "tt0133093"}}
}

func stop(eventID, historyID string) *v1.WatchSyncEvent {
	return &v1.WatchSyncEvent{EventId: eventID, Operation: v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP, OccurredAt: at("2026-10-14T12:34:56.789123456Z"), Media: movie(goldenMedia), WatchHistoryId: historyID, PlaybackSessionId: "session-1", PositionSeconds: 8100.6, DurationSeconds: 8160, Completed: true}
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var out T
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return out
}

func statuses(r *v1.WatchSyncApplyEventsResponse) []v1.WatchSyncApplyStatus {
	var out []v1.WatchSyncApplyStatus
	for _, result := range r.GetResults() {
		out = append(out, result.GetStatus())
	}
	return out
}
