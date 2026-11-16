package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func read(t *testing.T, f *fakePastime, r *v1.WatchSyncListRemoteStateRequest) *v1.WatchSyncListRemoteStateResponse {
	t.Helper()
	if r.Context == nil {
		r.Context = f.auth()
	}
	out, err := f.provider().ListRemoteState(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetUpdatedCredentials() != nil {
		t.Fatal("the plugin never rotates credentials")
	}
	return out
}

// Pastime's full snapshot reaches Silo as additions and named removals: never a
// complete snapshot, so an absent item never deletes a Silo item, and each
// removal is a tombstone keyed by the item's catalog key.
func TestRemoteStatePassesAdditionsAndNamedRemovals(t *testing.T) {
	f := newFake(t)
	f.queue("/state:read", answer{200, "", `{"items":[
 {"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{"tmdb":"603"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"},
 {"provider_item_key":"catalog:2","kind":"favorite","media_kind":"series","external_ids":{"tvdb":"81189"},"removed":true,"changed_at":"2026-10-01T10:00:00Z"},
 {"provider_item_key":"catalog:3","kind":"watchlist","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"position":4,"changed_at":"2026-10-01T10:00:00Z"},
 {"provider_item_key":"catalog:4","kind":"rating","media_kind":"movie","external_ids":{"tmdb":"9"},"removed":true,"changed_at":"2026-10-01T10:00:00Z"},
 {"provider_item_key":"catalog:5","kind":"rating","media_kind":"series","external_ids":{"tmdb":"7","imdb":"tt7"},"removed":false,"rating":8,"changed_at":"2026-10-01T10:00:00Z"}
],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":["Some titles were skipped."]}`})
	out := read(t, f, &v1.WatchSyncListRemoteStateRequest{Cursor: "full-v1", PageSize: 100})
	if out.Fault != nil || out.CompleteSnapshot || out.NextCursor != "full-v1" || out.NextPageToken != "" || len(out.Warnings) != 1 {
		t.Fatalf("%v", out)
	}
	if len(out.Items) != 5 {
		t.Fatalf("items %v", out.Items)
	}
	fav, unfav, watch, unrate, rating := out.Items[0], out.Items[1], out.Items[2], out.Items[3], out.Items[4]
	if unfav.ProviderItemKey != "catalog:2" || !unfav.Favorite.GetRemoved() || unfav.Media != nil || unfav.Watchlist != nil ||
		!unfav.Favorite.GetListedAt().AsTime().Equal(time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("favourite removal %v", unfav)
	}
	if unrate.ProviderItemKey != "catalog:4" || !unrate.Rating.GetRemoved() || unrate.Rating.GetRating() != 0 || unrate.Media != nil {
		t.Fatalf("rating removal %v", unrate)
	}
	if fav.ProviderItemKey != "catalog:1" || fav.Favorite == nil || fav.Favorite.Removed || fav.Media.MediaType != v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE || fav.Media.ExternalIds["tmdb"] != "603" {
		t.Fatalf("%v", fav)
	}
	if watch.Watchlist == nil || watch.Watchlist.Removed || watch.ProviderItemKey != "catalog:3" {
		t.Fatalf("%v", watch)
	}
	if rating.Rating == nil || rating.Rating.Rating != 8 || rating.Rating.Removed || rating.Media.MediaType != v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES {
		t.Fatalf("%v", rating)
	}
	in := decode[client.PersonalRead](t, f.sent("/state:read")[0])
	// No families from Silo means every family the plugin offers, named
	// explicitly so Pastime's own default (the three list families) never applies.
	if in.Cursor != "full-v1" || in.PageSize != 100 || strings.Join(in.Kinds, ",") != "favorite,watchlist,rating,watched,progress" {
		t.Fatalf("request %+v", in)
	}
}

func TestRemoteStatePagesPassThrough(t *testing.T) {
	f := newFake(t)
	f.queue("/state:read", answer{200, "", `{"items":[{"provider_item_key":"catalog:2","kind":"watchlist","media_kind":"series","external_ids":{"tvdb":"81189"},"removed":true,"changed_at":"2026-10-01T10:00:00Z"}],"next_page_token":"page-2","next_cursor":"","complete_snapshot":true,"warnings":[]}`})
	out := read(t, f, &v1.WatchSyncListRemoteStateRequest{PageToken: "page-1", PageSize: 1, StateKinds: []v1.WatchSyncRemoteStateKind{v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST}})
	if out.Fault != nil || len(out.Items) != 1 || !out.Items[0].Watchlist.GetRemoved() || out.CompleteSnapshot || out.NextPageToken != "page-2" || out.NextCursor != "" {
		t.Fatalf("%v", out)
	}
	in := decode[client.PersonalRead](t, f.sent("/state:read")[0])
	if len(in.Kinds) != 1 || in.Kinds[0] != "watchlist" || in.PageToken != "page-1" || in.PageSize != 1 {
		t.Fatalf("request %+v", in)
	}
}

func TestRemoteStateRejectsDataOutsideTheContract(t *testing.T) {
	for _, item := range []string{
		`{"provider_item_key":"imdb:tt1","kind":"favorite","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:0","kind":"favorite","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{"trakt":"1"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"episode","external_ids":{"imdb":"tt1"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"changed_at":"0001-01-01T00:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"rating","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"rating":7,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"rating":8,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"watched","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"watched","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"play_count":0,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"watched","media_kind":"series","external_ids":{"tvdb":"1"},"removed":false,"play_count":1,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"watched","media_kind":"episode","external_ids":{},"removed":false,"play_count":1,"season_number":1,"episode_number":2,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"watched","media_kind":"episode","external_ids":{},"series_external_ids":{"tvdb":"1"},"removed":false,"play_count":1,"episode_number":2,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"watched","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"play_count":1,"rating":8,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"watched","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"play_count":1,"progress_percent":5,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"progress","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"progress","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"progress_percent":100,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"progress","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"progress_percent":-1,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"progress","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"progress_percent":5,"play_count":1,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":false,"play_count":1,"changed_at":"2026-10-01T10:00:00Z"}`,
		`{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{"imdb":"tt1"},"series_external_ids":{"tvdb":"1"},"removed":false,"changed_at":"2026-10-01T10:00:00Z"}`,
	} {
		f := newFake(t)
		f.queue("/state:read", answer{200, "", `{"items":[` + item + `],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`})
		out := read(t, f, &v1.WatchSyncListRemoteStateRequest{})
		if out.Fault.GetCode() != cTemporary || out.Fault.GetSafeMessage() != msgUnreadable || len(out.Items) != 0 {
			t.Errorf("%s: %v", item, out)
		}
	}
	// An item of a family Silo did not ask for.
	f := newFake(t)
	f.queue("/state:read", answer{200, "", `{"items":[{"provider_item_key":"catalog:1","kind":"favorite","media_kind":"movie","external_ids":{"imdb":"tt1"},"removed":true,"changed_at":"2026-10-01T10:00:00Z"}],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`})
	out := read(t, f, &v1.WatchSyncListRemoteStateRequest{StateKinds: []v1.WatchSyncRemoteStateKind{v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_RATING}})
	if out.Fault.GetCode() != cTemporary {
		t.Fatalf("%v", out)
	}
}

func TestRemoteStateFaults(t *testing.T) {
	for _, c := range []struct {
		answer answer
		code   v1.WatchSyncFaultCode
	}{
		{answer{409, "", `{"code":"rating_precision"}`}, cPermanent},
		{answer{429, "60", `{"code":"rate_limited"}`}, cRateLimited},
		{answer{422, "", `{"code":"validation_error"}`}, cTemporary},
		{answer{500, "", ``}, cTemporary},
		{answer{401, "", `{"code":"unauthenticated"}`}, cCredential},
		{answer{403, "", `{"code":"service_access_required"}`}, cTemporary},
		{answer{410, "", `{"code":"gone"}`}, cTemporary},
	} {
		f := newFake(t)
		f.queue("/state:read", c.answer)
		if out := read(t, f, &v1.WatchSyncListRemoteStateRequest{}); out.Fault.GetCode() != c.code || len(out.Items) != 0 {
			t.Errorf("%d: %v", c.answer.status, out)
		}
	}
	f := newFake(t)
	for _, r := range []*v1.WatchSyncListRemoteStateRequest{
		{StateKinds: []v1.WatchSyncRemoteStateKind{v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_DROPPED}},
		{PageSize: 101},
		{PageSize: -1},
	} {
		if out := read(t, f, r); out.Fault.GetCode() != cInvalid {
			t.Errorf("%v: %v", r, out)
		}
	}
	if f.count() != 0 {
		t.Fatal("an unsupported read reached Pastime")
	}
}

// Watched plays and resume points from Pastime reach Silo as movies or as
// episodes named by their show, season and episode.
func TestRemoteStateReadsWatchedAndProgress(t *testing.T) {
	f := newFake(t)
	f.queue("/state:read", answer{200, "", `{"items":[
 {"provider_item_key":"catalog:11","kind":"watched","media_kind":"movie","external_ids":{"tmdb":"603","imdb":"tt0133093"},"removed":false,"play_count":2,"changed_at":"2026-03-01T20:00:00Z"},
 {"provider_item_key":"catalog:12","kind":"watched","media_kind":"episode","external_ids":{"tmdb":"63057"},"series_external_ids":{"tmdb":"1399","tvdb":"121361"},"season_number":1,"episode_number":2,"removed":false,"play_count":1,"changed_at":"2026-04-02T21:30:00Z"},
 {"provider_item_key":"catalog:13","kind":"progress","media_kind":"episode","external_ids":{},"series_external_ids":{"tvdb":"121361"},"season_number":0,"episode_number":1,"removed":false,"progress_percent":12.5,"changed_at":"2026-04-03T08:00:00Z"},
 {"provider_item_key":"catalog:14","kind":"progress","media_kind":"movie","external_ids":{"tmdb":"604"},"removed":false,"progress_percent":25,"changed_at":"2026-04-04T08:00:00Z"}
],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`})
	out := read(t, f, &v1.WatchSyncListRemoteStateRequest{PageSize: 100, StateKinds: []v1.WatchSyncRemoteStateKind{
		v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHED, v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_PROGRESS}})
	if out.Fault != nil || len(out.Items) != 4 || out.CompleteSnapshot {
		t.Fatalf("%v", out)
	}
	movie, ep, epProgress, movieProgress := out.Items[0], out.Items[1], out.Items[2], out.Items[3]
	if movie.Media.MediaType != v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE || movie.Watched.GetPlayCount() != 2 ||
		!movie.Watched.GetLastWatchedAt().AsTime().Equal(time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)) || movie.Media.ExternalIds["imdb"] != "tt0133093" || movie.Progress != nil {
		t.Fatalf("movie %v", movie)
	}
	if ep.Media.MediaType != v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_EPISODE || ep.Watched.GetPlayCount() != 1 || ep.Media.SeriesExternalIds["tvdb"] != "121361" ||
		ep.Media.SeasonNumber != 1 || ep.Media.EpisodeNumber != 2 || ep.Media.ExternalIds["tmdb"] != "63057" || ep.ProviderItemKey != "catalog:12" {
		t.Fatalf("episode %v", ep)
	}
	if epProgress.Progress.GetProgressPercent() != 12.5 || epProgress.Media.SeasonNumber != 0 || epProgress.Media.EpisodeNumber != 1 || epProgress.Watched != nil ||
		!epProgress.Progress.GetPausedAt().AsTime().Equal(time.Date(2026, 4, 3, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("episode progress %v", epProgress)
	}
	if movieProgress.Progress.GetProgressPercent() != 25 || movieProgress.Media.MediaType != v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE {
		t.Fatalf("movie progress %v", movieProgress)
	}
	in := decode[client.PersonalRead](t, f.sent("/state:read")[0])
	if strings.Join(in.Kinds, ",") != "watched,progress" {
		t.Fatalf("request %+v", in)
	}
}

// A movie or show the profile deleted from its Pastime library reaches Silo as
// a watched removal with the deletion time, beside list removals, and a removal
// outside Pastime's shape is refused.
func TestRemoteStatePassesWatchedRemovals(t *testing.T) {
	f := newFake(t)
	f.queue("/state:read", answer{200, "", `{"items":[
 {"provider_item_key":"removed:catalog:21","kind":"watched","media_kind":"series","external_ids":{"tmdb":"1399","tvdb":"121361"},"removed":true,"changed_at":"2026-11-04T21:00:00Z"},
 {"provider_item_key":"removed:catalog:22","kind":"watched","media_kind":"movie","external_ids":{"tmdb":"603"},"removed":true,"changed_at":"2026-11-04T21:05:00Z"},
 {"provider_item_key":"catalog:2","kind":"favorite","media_kind":"series","external_ids":{"tvdb":"81189"},"removed":true,"changed_at":"2026-10-01T10:00:00Z"}
],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`})
	out := read(t, f, &v1.WatchSyncListRemoteStateRequest{PageSize: 100})
	if out.Fault != nil || len(out.Items) != 3 || out.CompleteSnapshot {
		t.Fatalf("%v", out)
	}
	show, movie, unfav := out.Items[0], out.Items[1], out.Items[2]
	if !unfav.Favorite.GetRemoved() || unfav.Watched != nil {
		t.Fatalf("favourite removal %v", unfav)
	}
	if show.ProviderItemKey != "removed:catalog:21" || show.Media.MediaType != v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES || show.Media.ExternalIds["tvdb"] != "121361" ||
		!show.Watched.GetRemoved() || show.Watched.GetPlayCount() != 0 || !show.Watched.GetLastWatchedAt().AsTime().Equal(time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)) ||
		show.Favorite != nil || show.Progress != nil {
		t.Fatalf("show %v", show)
	}
	if movie.Media.MediaType != v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE || !movie.Watched.GetRemoved() || movie.Media.ExternalIds["tmdb"] != "603" {
		t.Fatalf("movie %v", movie)
	}
	for _, bad := range []string{
		`{"provider_item_key":"catalog:23","kind":"watched","media_kind":"movie","external_ids":{"tmdb":"603"},"removed":true,"changed_at":"2026-11-04T21:00:00Z"}`,
		`{"provider_item_key":"removed:catalog:23","kind":"watched","media_kind":"episode","external_ids":{"tmdb":"1"},"series_external_ids":{"tmdb":"2"},"season_number":1,"episode_number":1,"removed":true,"changed_at":"2026-11-04T21:00:00Z"}`,
		`{"provider_item_key":"removed:catalog:23","kind":"watched","media_kind":"movie","external_ids":{"tmdb":"603"},"removed":true,"play_count":1,"changed_at":"2026-11-04T21:00:00Z"}`,
		`{"provider_item_key":"removed:catalog:23","kind":"watched","media_kind":"movie","external_ids":{},"removed":true,"changed_at":"2026-11-04T21:00:00Z"}`,
		`{"provider_item_key":"removed:catalog:23","kind":"favorite","media_kind":"movie","external_ids":{"tmdb":"603"},"removed":true,"changed_at":"2026-11-04T21:00:00Z"}`,
		`{"provider_item_key":"catalog:23","kind":"rating","media_kind":"movie","external_ids":{"tmdb":"603"},"removed":true,"changed_at":"0001-01-01T00:00:00Z"}`,
	} {
		f.queue("/state:read", answer{200, "", `{"items":[` + bad + `],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`})
		if out := read(t, f, &v1.WatchSyncListRemoteStateRequest{PageSize: 100}); out.Fault == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
