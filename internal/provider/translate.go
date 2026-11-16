package provider

import (
	"math"
	"strings"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const (
	// maxSeconds is Pastime's bound for positions and durations.
	maxSeconds = 10_000_000
	// clockSkew is how far in the future Pastime accepts an occurrence.
	clockSkew = 5 * time.Minute
)

var idNamespaces = []string{"tmdb", "imdb", "tvdb"}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// occurredAt validates an event time against Pastime's bounds.
func occurredAt(e *v1.WatchSyncEvent, now time.Time) (time.Time, bool) {
	if e.GetOccurredAt() == nil || e.GetOccurredAt().CheckValid() != nil {
		return time.Time{}, false
	}
	at := e.GetOccurredAt().AsTime()
	return at, revision(at) != 0 && !at.After(now.Add(clockSkew))
}

// playbackEvent translates a scrobble or a history export (MARK_WATCHED) into
// Pastime's playback event. A completion carries the consumption ID of its
// Silo watch-history row, so a live stop, a history export and any retry of
// one viewing record a single play. Like the former host, it refuses a
// completion without a watch-history ID.
func playbackEvent(b client.Binding, e *v1.WatchSyncEvent, now time.Time) (client.PlaybackEvent, *v1.WatchSyncFault) {
	var out client.PlaybackEvent
	invalid := invalidRequest("Silo sent a playback event Pastime cannot record.")
	if e.GetMedia().GetMediaItemId() == "" {
		return out, invalid
	}
	at, ok := occurredAt(e, now)
	if !ok {
		return out, invalid
	}
	completed := e.GetCompleted()
	switch e.GetOperation() {
	case v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_START:
		out.Operation = "start"
	case v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_PAUSE:
		out.Operation = "pause"
	case v1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP:
		out.Operation = "stop"
	case v1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED:
		out.Operation, completed = "stop", true
	default:
		return out, invalid
	}
	if completed && (out.Operation != "stop" || e.GetWatchHistoryId() == "") {
		return out, invalid
	}
	position, duration := e.GetPositionSeconds(), e.GetDurationSeconds()
	if !finite(position) || !finite(duration) || position < 0 || position > maxSeconds || duration > maxSeconds {
		return out, invalid
	}
	if duration < 1 {
		// History rows can lack a runtime. Pastime needs one, and for a
		// completion it only bounds the stored position.
		if !completed {
			return out, invalid
		}
		duration = 1
	}
	media := client.PlaybackMedia{ExternalIDs: knownIDs(e.GetMedia().GetExternalIds())}
	switch e.GetMedia().GetMediaType() {
	case v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE:
		media.Kind = "movie"
		if len(media.ExternalIDs) == 0 {
			return out, invalid
		}
	case v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_EPISODE:
		media.Kind = "episode"
		media.SeriesExternalIDs = knownIDs(e.GetMedia().GetSeriesExternalIds())
		season, episode := e.GetMedia().GetSeasonNumber(), e.GetMedia().GetEpisodeNumber()
		if season < 0 || episode < 1 || len(media.SeriesExternalIDs) == 0 {
			return out, invalid
		}
		media.Season, media.Episode = &season, &episode
		media.Title, media.SeriesTitle = boundedTitle(e.GetMedia().GetTitle()), boundedTitle(e.GetMedia().GetSeriesTitle())
	default:
		return out, invalid
	}
	out.Version = 1
	out.InstanceID, out.AccountID, out.ProfileID = b.InstanceID, b.AccountID, b.ProfileID
	out.EntityID = playbackEntityID(e.GetMedia().GetMediaItemId())
	out.Revision = revision(at)
	out.OccurredAt = at
	out.Completed = completed
	if completed {
		out.ConsumptionID = consumptionID(b.AccountID, b.ProfileID, e.GetWatchHistoryId())
	}
	out.PositionSeconds, out.DurationSeconds = int(position), int(duration)
	out.Media = media
	if out.EventID = wireEventID(out); out.EventID == "" {
		return out, invalid
	}
	return out, nil
}

// maxTitleRunes is the longest title Pastime accepts.
const maxTitleRunes = 500

// boundedTitle trims a title and cuts it to the length Pastime accepts.
func boundedTitle(title string) string {
	title = strings.TrimSpace(title)
	if r := []rune(title); len(r) > maxTitleRunes {
		title = strings.TrimSpace(string(r[:maxTitleRunes]))
	}
	return title
}

// knownIDs keeps the non-empty tmdb, imdb and tvdb IDs.
func knownIDs(ids map[string]string) map[string]string {
	out := map[string]string{}
	for _, ns := range idNamespaces {
		if v := ids[ns]; v != "" {
			out[ns] = v
		}
	}
	return out
}
