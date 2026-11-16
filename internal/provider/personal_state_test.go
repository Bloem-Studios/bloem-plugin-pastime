package provider

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func personal(id string, op v1.WatchSyncOperation) *v1.WatchSyncEvent {
	return &v1.WatchSyncEvent{EventId: id, Operation: op, Origin: v1.WatchSyncOrigin_WATCH_SYNC_ORIGIN_MANUAL, OccurredAt: at("2026-10-02T08:00:00Z"), Media: movie(goldenMedia)}
}

func TestPersonalChangesUseLegacyEntitiesAndNoPosition(t *testing.T) {
	f := newFake(t)
	favorite := personal("WATCH_SYNC_OPERATION_ADD_FAVORITE:"+goldenMedia, v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE)
	watchlist := personal("WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST:"+goldenMedia, v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST)
	position := int32(3)
	watchlist.ListPosition = &position
	rating := personal("WATCH_SYNC_OPERATION_SET_RATING:x", v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING)
	rating.Rating = 8
	out := apply(t, f, favorite, watchlist, rating)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sApplied, sApplied, sApplied}) {
		t.Fatalf("%v", out)
	}
	sent := f.sent("/state/events:apply")
	want := []struct{ op, entity string }{{"favorite_add", goldenFavorite}, {"watchlist_add", goldenWatchlist}, {"rating_set", goldenRating}}
	for i, body := range sent {
		e := decode[client.PersonalEvent](t, body)
		if e.Operation != want[i].op || e.EntityID != want[i].entity || e.Revision != revision(at("2026-10-02T08:00:00Z").AsTime()) || e.Media.Kind != "movie" || e.Media.CatalogItemID != "" || len(e.Media.ExternalIDs) != 2 {
			t.Fatalf("%d: %s", i, body)
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["position"]; ok {
			t.Fatalf("watchlist position sent: %s", body)
		}
		wire := e.EventID
		e.EventID = ""
		if wire != wireEventID(e) {
			t.Fatal("personal wire ID is not the payload digest")
		}
	}
	if r := decode[client.PersonalEvent](t, sent[2]).Rating; r == nil || *r != 8 {
		t.Fatal("rating value missing")
	}
}

// Removals Silo schedules carry no time and no external IDs, only the key the
// item was imported under.
func TestScheduledRemovalsResolveTheirKey(t *testing.T) {
	f := newFake(t)
	removal := func(id, key string, op v1.WatchSyncOperation, kind v1.WatchSyncMediaType) *v1.WatchSyncEvent {
		return &v1.WatchSyncEvent{EventId: id, Operation: op, Media: &v1.WatchSyncMedia{MediaItemId: "item-" + id, MediaType: kind, Title: "x"}, ProviderItemKey: key}
	}
	fav := v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FAVORITE
	movieType, seriesType := v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES
	out := apply(t, f,
		removal("imdb", "imdb:tt0133093", fav, movieType),
		removal("tmdb", "tmdb:603", v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST, movieType),
		removal("tvdb", "tvdb:81189", v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_RATING, seriesType),
		removal("catalog", "catalog:42", fav, seriesType),
		removal("bad-imdb", "imdb:nm123", fav, movieType),
		removal("episode-key", "show:tvdb:81189:s1:e2", fav, movieType),
		removal("no-key", "", fav, movieType),
		removal("bad-catalog", "catalog:042", fav, movieType),
		removal("episode", "tvdb:1", fav, v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_EPISODE),
	)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sApplied, sApplied, sApplied, sApplied, sRejected, sRejected, sRejected, sRejected, sRejected}) {
		t.Fatalf("%v", statuses(out))
	}
	sent := f.sent("/state/events:apply")
	if len(sent) != 4 {
		t.Fatalf("sent %d", len(sent))
	}
	nowMicro := revision(testNow)
	checks := []struct {
		op, lane, kind, ns, value, catalog string
	}{
		{"favorite_remove", "favorite", "movie", "imdb", "tt0133093", ""},
		{"watchlist_remove", "watchlist", "movie", "tmdb", "603", ""},
		{"rating_remove", "rating", "series", "tvdb", "81189", ""},
		{"favorite_remove", "favorite", "series", "", "", "42"},
	}
	for i, c := range checks {
		e := decode[client.PersonalEvent](t, sent[i])
		if e.Operation != c.op || e.Media.Kind != c.kind || e.Media.CatalogItemID != c.catalog || e.Rating != nil {
			t.Fatalf("%d: %s", i, sent[i])
		}
		if c.ns != "" && (len(e.Media.ExternalIDs) != 1 || e.Media.ExternalIDs[c.ns] != c.value) || c.ns == "" && len(e.Media.ExternalIDs) != 0 {
			t.Fatalf("%d ids: %s", i, sent[i])
		}
		// No time from Silo: the receive time, to the microsecond, is both
		// the occurrence and the revision.
		if e.Revision != nowMicro || e.OccurredAt.UnixMicro() != int64(nowMicro) || e.EntityID != personalEntityID(c.lane, "item-"+[]string{"imdb", "tmdb", "tvdb", "catalog"}[i]) {
			t.Fatalf("%d time/entity: %s", i, sent[i])
		}
	}
	var raw struct {
		Media map[string]any `json:"media"`
	}
	if err := json.Unmarshal(sent[3], &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Media["catalog_item_id"] != "42" {
		t.Fatalf("catalog ID must be a decimal string: %s", sent[3])
	}
}

func TestRatingsMustBeWholeStars(t *testing.T) {
	f := newFake(t)
	var events []*v1.WatchSyncEvent
	for _, value := range []int32{7, 0, 12, 2, 10} {
		e := personal("rating", v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING)
		e.Rating = value
		events = append(events, e)
	}
	out := apply(t, f, events...)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sRejected, sRejected, sRejected, sApplied, sApplied}) {
		t.Fatalf("%v", statuses(out))
	}
}

func TestPersonalResultsAndRejections(t *testing.T) {
	f := newFake(t)
	f.queue("/state/events:apply",
		answer{200, "", `{"status":"no_change","play_recorded":false}`},
		answer{200, "", `{"status":"stale","play_recorded":false}`},
		answer{409, "", `{"code":"conflict"}`},
		answer{422, "", `{"code":"validation_error"}`},
	)
	op := v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE
	playbackShaped := personal("with-history", op)
	playbackShaped.WatchHistoryId = "h"
	out := apply(t, f, personal("a", op), personal("b", op), personal("c", op), personal("d", op), personal("e", op), playbackShaped)
	if !slices.Equal(statuses(out), []v1.WatchSyncApplyStatus{sNoChange, sNoChange, sRejected, sRejected, sApplied, sRejected}) {
		t.Fatalf("%v", statuses(out))
	}
	if out.Results[2].Fault.Code != cPermanent || out.Results[3].Fault.Code != cInvalid {
		t.Fatalf("%v", out.Results)
	}
}
