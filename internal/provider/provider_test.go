package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/proto"
)

func apply(t *testing.T, f *fakePastime, events ...*v1.WatchSyncEvent) *v1.WatchSyncApplyEventsResponse {
	t.Helper()
	out, err := f.provider().ApplyEvents(context.Background(), &v1.WatchSyncApplyEventsRequest{Context: f.auth(), Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if out.GetUpdatedCredentials() != nil {
		t.Fatal("the plugin never rotates credentials")
	}
	for _, r := range out.GetResults() {
		if strings.Contains(r.GetFault().GetSafeMessage(), "ptbs_") {
			t.Fatal("a fault carries the key")
		}
	}
	return out
}

// A completed play carries the legacy entity and consumption IDs, so Pastime
// recognises a replay of a play the former integration already delivered.
func TestCompletedPlayCarriesLegacyIDs(t *testing.T) {
	f := newFake(t)
	out := apply(t, f, stop("scrobble:WATCH_SYNC_OPERATION_SCROBBLE_STOP:session-1", goldenHistory))
	if out.Fault != nil || !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sApplied}) || out.Results[0].EventId != "scrobble:WATCH_SYNC_OPERATION_SCROBBLE_STOP:session-1" {
		t.Fatalf("%v", out)
	}
	body := f.sent("/events:apply")[0]
	e := decode[client.PlaybackEvent](t, body)
	if e.EntityID != goldenMediaID || e.ConsumptionID != goldenPlay || e.Revision != goldenRevision {
		t.Fatalf("IDs %s %s %d", e.EntityID, e.ConsumptionID, e.Revision)
	}
	if e.Version != 1 || e.InstanceID != "instance-1" || e.AccountID != goldenAccount || e.ProfileID != goldenProfile || e.Operation != "stop" || !e.Completed || e.PositionSeconds != 8100 || e.DurationSeconds != 8160 {
		t.Fatalf("payload %s", body)
	}
	if e.Media.Kind != "movie" || e.Media.ExternalIDs["tmdb"] != "603" || e.Media.ExternalIDs["imdb"] != "tt0133093" || e.Media.Season != nil {
		t.Fatalf("media %+v", e.Media)
	}
	wire := e.EventID
	e.EventID = ""
	if !strings.HasPrefix(wire, "pevt_") || wire != wireEventID(e) {
		t.Fatalf("wire event ID %s is not the payload digest", wire)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["completed"]; !ok {
		t.Fatal("completion must be explicit")
	}
}

func TestReplayedOrStalePlaybackIsNoChange(t *testing.T) {
	f := newFake(t)
	f.queue("/events:apply",
		answer{200, "", `{"status":"applied","play_recorded":false}`},
		answer{200, "", `{"status":"stale","play_recorded":true}`},
		answer{200, "", `{"status":"applied","play_recorded":false}`},
	)
	start := stop("start", "")
	start.Operation, start.Completed = v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_START, false
	out := apply(t, f, stop("a", goldenHistory), stop("b", goldenHistory), start)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sNoChange, sNoChange, sApplied}) {
		t.Fatalf("%v", statuses(out))
	}
}

func TestHistoryExportsAndCompletionRules(t *testing.T) {
	f := newFake(t)
	export := &v1.WatchSyncEvent{EventId: goldenHistory, Operation: v1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED, OccurredAt: at("2026-10-01T20:00:00Z"), Media: movie(goldenMedia), WatchHistoryId: goldenHistory}
	noHistory := proto.Clone(export).(*v1.WatchSyncEvent)
	noHistory.EventId, noHistory.WatchHistoryId = "no-history", ""
	stopNoHistory := stop("stop-no-history", "")
	completedStart := stop("completed-start", goldenHistory)
	completedStart.Operation = v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_START
	unknownDuration := stop("unknown-duration", "")
	unknownDuration.Completed, unknownDuration.DurationSeconds = false, 0
	out := apply(t, f, export, noHistory, stopNoHistory, completedStart, unknownDuration)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sApplied, sRejected, sRejected, sRejected, sRejected}) {
		t.Fatalf("%v", statuses(out))
	}
	for _, r := range out.Results[1:] {
		if r.Fault.GetCode() != cInvalid {
			t.Fatalf("%v", r)
		}
	}
	sent := f.sent("/events:apply")
	if len(sent) != 1 {
		t.Fatalf("rejected events were sent: %d", len(sent))
	}
	e := decode[client.PlaybackEvent](t, sent[0])
	// A history row without a runtime still records the play.
	if !e.Completed || e.Operation != "stop" || e.DurationSeconds != 1 || e.PositionSeconds != 0 || e.ConsumptionID != goldenPlay || !e.OccurredAt.Equal(export.OccurredAt.AsTime()) {
		t.Fatalf("export %s", sent[0])
	}
}

func TestEpisodesNeedSeriesIDsAndNumbers(t *testing.T) {
	f := newFake(t)
	episode := stop("episode", goldenHistory)
	episode.Media = &v1.WatchSyncMedia{MediaItemId: "ep-1", MediaType: v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_EPISODE, ExternalIds: map[string]string{"tvdb": "349232"}, SeriesExternalIds: map[string]string{"tvdb": "81189", "imdb": ""}, SeasonNumber: 0, EpisodeNumber: 3,
		Title: "  Pilot  ", SeriesTitle: "Breaking Bad " + strings.Repeat("x", 600)}
	noSeries := proto.Clone(episode).(*v1.WatchSyncEvent)
	noSeries.EventId, noSeries.Media.SeriesExternalIds = "no-series", nil
	noEpisode := proto.Clone(episode).(*v1.WatchSyncEvent)
	noEpisode.EventId, noEpisode.Media.EpisodeNumber = "no-episode", 0
	out := apply(t, f, episode, noSeries, noEpisode)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sApplied, sRejected, sRejected}) {
		t.Fatalf("%v", statuses(out))
	}
	e := decode[client.PlaybackEvent](t, f.sent("/events:apply")[0])
	if e.Media.Kind != "episode" || *e.Media.Season != 0 || *e.Media.Episode != 3 || len(e.Media.SeriesExternalIDs) != 1 || e.EntityID != playbackEntityID("ep-1") {
		t.Fatalf("%+v", e)
	}
	// The titles go along, trimmed and cut to what Pastime accepts.
	if e.Media.Title != "Pilot" || len([]rune(e.Media.SeriesTitle)) != maxTitleRunes || !strings.HasPrefix(e.Media.SeriesTitle, "Breaking Bad x") {
		t.Fatalf("titles %q %q", e.Media.Title, e.Media.SeriesTitle)
	}
}

// Silo reuses event IDs for different payloads; Pastime refuses a known ID
// with a different payload. The wire ID follows the payload.
func TestWireIDsFollowThePayload(t *testing.T) {
	f := newFake(t)
	first := stop("scrobble:START:s1", "")
	first.Operation, first.Completed = v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_START, false
	retry := proto.Clone(first).(*v1.WatchSyncEvent)
	resumed := proto.Clone(first).(*v1.WatchSyncEvent)
	resumed.OccurredAt = at("2026-10-14T12:50:00Z")
	apply(t, f, first, retry, resumed)
	var ids []string
	for _, b := range f.sent("/events:apply") {
		ids = append(ids, decode[client.PlaybackEvent](t, b).EventID)
	}
	if len(ids) != 3 || ids[0] != ids[1] || ids[0] == ids[2] {
		t.Fatalf("wire IDs %v", ids)
	}
}

func TestCatalogLoadingRetriesOnlyThatEvent(t *testing.T) {
	f := newFake(t)
	f.queue("/events:apply", answer{503, "60", `{"code":"catalog_loading","message":"x"}`}, answer{404, "", `{"code":"no_match"}`})
	out := apply(t, f, stop("a", "h1"), stop("b", "h2"), stop("c", "h3"))
	if out.Fault != nil || !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sRetry, sRejected, sApplied}) {
		t.Fatalf("%v", out)
	}
	if r := out.Results[0]; r.Fault.Code != cTemporary || r.Fault.RetryAfter.AsDuration() != time.Minute || r.Fault.SafeMessage != msgCatalog {
		t.Fatalf("%v", r)
	}
	if r := out.Results[1]; r.Fault.Code != cPermanent || r.Fault.SafeMessage != msgNoMatch {
		t.Fatalf("%v", r)
	}
}

// A service failure is a batch fault: Silo marks nothing and spends no
// attempt, unlike 0.1.14, which made every later event RETRY/TEMPORARY.
func TestServiceFailureFailsTheWholeBatch(t *testing.T) {
	for _, a := range []answer{{502, "", ``}, {503, "", `{"code":"provider_unavailable"}`}, {504, "", ``}, {200, "", `{"status":"applied","play_recorded":true,"extra":1}`}} {
		f := newFake(t)
		f.queue("/events:apply", answer{200, "", `{"status":"applied","play_recorded":true}`}, a)
		out := apply(t, f, stop("a", "h1"), stop("b", "h2"), stop("c", "h3"))
		if out.Fault.GetCode() != cTemporary || len(out.Results) != 0 {
			t.Fatalf("%d: %v", a.status, out)
		}
		if n := len(f.sent("/events:apply")); n != 2 {
			t.Fatalf("kept sending after an outage: %d", n)
		}
	}
}

// A Pastime 500 for one event while Pastime is up retries only that event,
// so it cannot hold the oldest exports of a connection forever: Silo spends
// one of the event's five attempts per run and the rest continue.
func TestInternalErrorRetriesOnlyThatEvent(t *testing.T) {
	for _, path := range []string{"/events:apply", "/state/events:apply"} {
		f := newFake(t)
		f.queue(path, answer{200, "", `{"status":"applied","play_recorded":true}`}, answer{500, "", `{"code":"internal_error"}`})
		events := []*v1.WatchSyncEvent{stop("a", "h1"), stop("b", "h2"), stop("c", "h3")}
		if path == "/state/events:apply" {
			for i, e := range events {
				events[i] = &v1.WatchSyncEvent{EventId: e.EventId, Operation: v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE, OccurredAt: e.OccurredAt, Media: movie(fmt.Sprint("item-", i))}
			}
		}
		out := apply(t, f, events...)
		if out.Fault != nil || !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sApplied, sRetry, sApplied}) {
			t.Fatalf("%s: %v", path, out)
		}
		if r := out.Results[1]; r.Fault.GetCode() != cTemporary || r.Fault.GetSafeMessage() != msgEventFailed {
			t.Fatalf("%s: %v", path, r)
		}
		if n := len(f.sent("/credentials:inspect")); n != 2 {
			t.Fatalf("%s: inspect calls %d, want the batch check and one probe", path, n)
		}
	}
}

// When Pastime does not answer the probe after a 500, the 500 concerns the
// service: a batch fault, nothing marked.
func TestInternalErrorDuringAnOutageFailsTheBatch(t *testing.T) {
	for _, probe := range []answer{{503, "", `{"code":"unavailable"}`}, {401, "", `{"code":"unauthenticated"}`}} {
		f := newFake(t)
		f.queue("/credentials:inspect", f.bindingAnswer(), probe)
		f.queue("/events:apply", answer{500, "", `{"code":"internal_error"}`})
		out := apply(t, f, stop("a", "h1"), stop("b", "h2"))
		if out.Fault.GetCode() != cTemporary || len(out.Results) != 0 || len(f.sent("/events:apply")) != 1 {
			t.Fatalf("probe %d: %v", probe.status, out)
		}
	}
	f := newFake(t)
	other := f.binding
	other.ProfileID = "another-profile"
	b, _ := json.Marshal(other)
	f.queue("/credentials:inspect", f.bindingAnswer(), answer{200, "", string(b)})
	f.queue("/events:apply", answer{500, "", `{"code":"internal_error"}`})
	if out := apply(t, f, stop("a", "h1"), stop("b", "h2")); out.Fault.GetCode() != cTemporary || len(out.Results) != 0 {
		t.Fatalf("rebound key: %v", out)
	}
}

// Silo gives a watch-sync call two minutes. Events that no longer fit are
// returned for the next run without an attempt, so the results so far reach
// Silo instead of an expired call that records nothing.
func TestDeadlineDefersTheRest(t *testing.T) {
	f := newFake(t)
	f.delay = 400 * time.Millisecond
	p := f.provider()
	p.reserve = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	events := make([]*v1.WatchSyncEvent, 8)
	for i := range events {
		events[i] = stop(fmt.Sprint("e", i), fmt.Sprint("h", i))
	}
	out, err := p.ApplyEvents(ctx, &v1.WatchSyncApplyEventsRequest{Context: f.auth(), Events: events})
	if err != nil || out.Fault != nil || len(out.Results) != len(events) {
		t.Fatalf("%v %v", err, out)
	}
	if ctx.Err() != nil {
		t.Fatal("the answer missed the deadline")
	}
	applied := 0
	for i, r := range out.Results {
		switch {
		case r.Status == sApplied && applied == i:
			applied++
		case r.Status == sRetry && r.Fault.GetCode() == cRateLimited && r.Fault.GetRetryAfter().AsDuration() == time.Second && r.Fault.GetSafeMessage() == msgNextRun:
		default:
			t.Fatalf("result %d: %v", i, r)
		}
	}
	if applied == 0 || applied == len(events) || len(f.sent("/events:apply")) != applied {
		t.Fatalf("applied %d, sent %d", applied, len(f.sent("/events:apply")))
	}
}

func TestNoTimeLeftSendsNothing(t *testing.T) {
	f := newFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := f.provider().ApplyEvents(ctx, &v1.WatchSyncApplyEventsRequest{Context: f.auth(), Events: []*v1.WatchSyncEvent{stop("a", "h1"), stop("b", "h2")}})
	if out.Fault != nil || !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sRetry, sRetry}) || len(f.sent("/events:apply")) != 0 {
		t.Fatalf("%v", out)
	}
}

func TestRateLimitDefersTheRestWithoutSending(t *testing.T) {
	f := newFake(t)
	f.queue("/events:apply", answer{200, "", `{"status":"applied","play_recorded":true}`}, answer{429, "30", `{"code":"rate_limited"}`})
	out := apply(t, f, stop("a", "h1"), stop("b", "h2"), stop("c", "h3"))
	if out.Fault != nil || !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sApplied, sRetry, sRetry}) {
		t.Fatalf("%v", out)
	}
	for _, r := range out.Results[1:] {
		if r.Fault.Code != cRateLimited || r.Fault.RetryAfter.AsDuration() != 30*time.Second {
			t.Fatalf("%v", r)
		}
	}
	if n := len(f.sent("/events:apply")); n != 2 {
		t.Fatalf("sent after the rate limit: %d", n)
	}
}

func TestConnectionFailures(t *testing.T) {
	for name, c := range map[string]struct {
		path   string
		answer answer
		code   v1.WatchSyncFaultCode
		msg    string
	}{
		"inspect 401":   {"/credentials:inspect", answer{401, "", `{"code":"unauthenticated"}`}, cCredential, msgReconnect},
		"inspect 403":   {"/credentials:inspect", answer{403, "", `{"code":"service_access_required"}`}, cTemporary, msgNoAccess},
		"inspect 410":   {"/credentials:inspect", answer{410, "", `{"code":"gone"}`}, cTemporary, msgGone},
		"inspect 429":   {"/credentials:inspect", answer{429, "60", `{"code":"rate_limited"}`}, cRateLimited, msgRateLimited},
		"inspect 502":   {"/credentials:inspect", answer{502, "", ``}, cTemporary, msgUnavailable},
		"inspect 404":   {"/credentials:inspect", answer{404, "", `{"code":"not_found"}`}, cTemporary, msgNotPastime},
		"apply 401":     {"/events:apply", answer{401, "", `{"code":"unauthenticated"}`}, cCredential, msgReconnect},
		"apply 403":     {"/events:apply", answer{403, "", `{"code":"forbidden"}`}, cCredential, msgOtherProfile},
		"apply 403 sar": {"/events:apply", answer{403, "", `{"code":"service_access_required"}`}, cTemporary, msgNoAccess},
		"apply 410":     {"/events:apply", answer{410, "", `{"code":"gone"}`}, cTemporary, msgGone},
		"apply 404":     {"/events:apply", answer{404, "", `{"code":"unknown_route"}`}, cTemporary, msgNotPastime},
	} {
		f := newFake(t)
		f.queue(c.path, c.answer)
		out := apply(t, f, stop("a", "h1"), stop("b", "h2"))
		if out.Fault.GetCode() != c.code || out.Fault.GetSafeMessage() != c.msg || len(out.Results) != 0 {
			t.Errorf("%s: %v", name, out)
		}
	}
}

func TestStoredCredentialsMustMatchPastimeBinding(t *testing.T) {
	f := newFake(t)
	a := f.auth()
	a.Credentials.SecretAttributes["profile_id"] = "another-profile"
	out, _ := f.provider().ApplyEvents(context.Background(), &v1.WatchSyncApplyEventsRequest{Context: a, Events: []*v1.WatchSyncEvent{stop("a", "h1")}})
	if out.Fault.GetCode() != cCredential || out.Fault.GetSafeMessage() != msgOtherProfile || len(f.sent("/events:apply")) != 0 {
		t.Fatalf("%v", out)
	}
	for _, mutate := range []func(*v1.WatchSyncAuthenticatedContext){
		func(a *v1.WatchSyncAuthenticatedContext) { a.Credentials.AccessToken = "ptbp_provisioning" },
		func(a *v1.WatchSyncAuthenticatedContext) { a.Credentials = nil },
	} {
		a := f.auth()
		mutate(a)
		out, _ := f.provider().ApplyEvents(context.Background(), &v1.WatchSyncApplyEventsRequest{Context: a, Events: []*v1.WatchSyncEvent{stop("a", "h1")}})
		if out.Fault.GetCode() != cCredential {
			t.Fatalf("%v", out)
		}
	}
	a = f.auth()
	a.CapabilityId = "trakt"
	if out, _ := f.provider().ApplyEvents(context.Background(), &v1.WatchSyncApplyEventsRequest{Context: a, Events: []*v1.WatchSyncEvent{stop("a", "h1")}}); out.Fault.GetCode() != cInvalid {
		t.Fatalf("%v", out)
	}
}

// Without an address nothing is sent and nothing is marked: exports stay
// pending with no attempt spent until the administrator sets it.
func TestAddressSettingIsRequired(t *testing.T) {
	f := newFake(t)
	for value, msg := range map[string]string{"": msgNoAddress, "https://pastime.example.com/sub": msgBadAddress, "http://pastime.example.com": msgBadAddress} {
		a := f.auth()
		a.ProviderConfig.Values["pastime.pastime_url"] = value
		out, _ := f.provider().ApplyEvents(context.Background(), &v1.WatchSyncApplyEventsRequest{Context: a, Events: []*v1.WatchSyncEvent{stop("a", "h1")}})
		if out.Fault.GetCode() != cTemporary || out.Fault.GetSafeMessage() != msg {
			t.Errorf("%q: %v", value, out)
		}
	}
	if f.count() != 0 {
		t.Fatal("sent without a usable address")
	}
}

func TestBatchBounds(t *testing.T) {
	f := newFake(t)
	events := make([]*v1.WatchSyncEvent, 101)
	for i := range events {
		events[i] = stop(fmt.Sprint(i), fmt.Sprint(i))
	}
	for _, batch := range [][]*v1.WatchSyncEvent{nil, events} {
		out, _ := f.provider().ApplyEvents(context.Background(), &v1.WatchSyncApplyEventsRequest{Context: f.auth(), Events: batch})
		if out.Fault.GetCode() != cInvalid {
			t.Fatalf("%d events: %v", len(batch), out)
		}
	}
	out := apply(t, f, events[:100]...)
	if len(out.Results) != 100 || out.Fault != nil {
		t.Fatalf("%v", out.Fault)
	}
	if out := apply(t, f, nil, stop("x", "h")); !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sRejected, sApplied}) {
		t.Fatalf("nil event: %v", statuses(out))
	}
}

func TestFutureAndPreEpochOccurrencesAreRejected(t *testing.T) {
	f := newFake(t)
	future := stop("future", "h1")
	future.OccurredAt = at("2026-10-14T13:06:00Z")
	epoch := stop("epoch", "h2")
	epoch.OccurredAt = at("1970-01-01T00:00:00Z")
	nearFuture := stop("near-future", "h3")
	nearFuture.OccurredAt = at("2026-10-14T13:04:00Z")
	out := apply(t, f, future, epoch, nearFuture)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sRejected, sRejected, sApplied}) {
		t.Fatalf("%v", statuses(out))
	}
}
