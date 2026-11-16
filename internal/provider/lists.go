package provider

import (
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// personalLane is the personal-state lane an operation writes, or "".
func personalLane(op v1.WatchSyncOperation) string {
	switch op {
	case v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE, v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FAVORITE:
		return "favorite"
	case v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST, v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST:
		return "watchlist"
	case v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING, v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_RATING:
		return "rating"
	}
	return ""
}

var personalOperations = map[v1.WatchSyncOperation]string{
	v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_FAVORITE:          "favorite_add",
	v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FAVORITE:       "favorite_remove",
	v1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST:      "watchlist_add",
	v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST: "watchlist_remove",
	v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SET_RATING:            "rating_set",
	v1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_RATING:         "rating_remove",
}

// personalEvent translates a favourite, watchlist or rating change. Silo
// sends removals and some rating writes without a time; they take now, which
// also becomes their revision. A watchlist position is never sent (see
// client.PersonalEvent).
func personalEvent(b client.Binding, e *v1.WatchSyncEvent, now time.Time) (client.PersonalEvent, *v1.WatchSyncFault) {
	var out client.PersonalEvent
	invalid := invalidRequest("Silo sent a favourite, watchlist or rating change Pastime cannot record.")
	lane, operation := personalLane(e.GetOperation()), personalOperations[e.GetOperation()]
	if lane == "" || e.GetMedia().GetMediaItemId() == "" || e.GetCompleted() || e.GetPlaybackSessionId() != "" || e.GetWatchHistoryId() != "" || e.GetPositionSeconds() != 0 || e.GetDurationSeconds() != 0 {
		return out, invalid
	}
	at := now
	if e.GetOccurredAt() != nil {
		var ok bool
		if at, ok = occurredAt(e, now); !ok {
			return out, invalid
		}
	}
	if operation == "rating_set" {
		// Pastime stores whole stars, sent as even values on the ten-point
		// scale. An odd Silo rating has no exact equivalent.
		if e.GetRating() < 2 || e.GetRating() > 10 || e.GetRating()%2 != 0 {
			return out, invalidRequest("Pastime stores whole stars. Use an even rating from 2 to 10 to sync it.")
		}
		rating := int(e.GetRating())
		out.Rating = &rating
	}
	media, ok := personalMedia(e)
	if !ok {
		return out, invalidRequest("Silo sent no TMDB, IMDb or TVDB ID Pastime can use for this title.")
	}
	out.Version = 1
	out.InstanceID, out.AccountID, out.ProfileID = b.InstanceID, b.AccountID, b.ProfileID
	out.EntityID = personalEntityID(lane, e.GetMedia().GetMediaItemId())
	out.Revision = revision(at)
	out.Operation = operation
	out.OccurredAt = at
	out.Media = media
	if out.EventID = wireEventID(out); out.EventID == "" {
		return out, invalid
	}
	return out, nil
}

// personalMedia names the title by its external IDs. Removals Silo schedules
// carry none, only the key the item was imported under: an "imdb:", "tmdb:"
// or "tvdb:" key is that ID, and a "catalog:" key is Pastime's own catalog ID,
// which Pastime release A accepts in place of external IDs.
func personalMedia(e *v1.WatchSyncEvent) (client.PersonalMedia, bool) {
	media := client.PersonalMedia{ExternalIDs: knownIDs(e.GetMedia().GetExternalIds())}
	switch e.GetMedia().GetMediaType() {
	case v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE:
		media.Kind = "movie"
	case v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES:
		media.Kind = "series"
	default:
		return media, false
	}
	if len(media.ExternalIDs) > 0 {
		return media, true
	}
	ns, value, _ := strings.Cut(e.GetProviderItemKey(), ":")
	switch {
	case ns == "imdb" && imdbID(value), (ns == "tmdb" || ns == "tvdb") && positiveDecimal(value):
		media.ExternalIDs[ns] = value
	case ns == "catalog" && positiveDecimal(value):
		media.CatalogItemID = value
	default:
		return media, false
	}
	return media, true
}

func positiveDecimal(v string) bool {
	n, err := strconv.ParseInt(v, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == v
}

// imdbID matches Pastime's ^tt[0-9]{1,18}$.
func imdbID(v string) bool {
	digits, ok := strings.CutPrefix(v, "tt")
	if !ok || len(digits) < 1 || len(digits) > 18 {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}
