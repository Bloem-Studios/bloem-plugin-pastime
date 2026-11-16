package provider

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var remoteKinds = map[v1.WatchSyncRemoteStateKind]string{
	v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_FAVORITE:  "favorite",
	v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHLIST: "watchlist",
	v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_RATING:    "rating",
	v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_WATCHED:   "watched",
	v1.WatchSyncRemoteStateKind_WATCH_SYNC_REMOTE_STATE_KIND_PROGRESS:  "progress",
}

// allRemoteKinds is what an empty request from Silo means: every family the
// plugin offers. It is sent explicitly because Pastime reads an empty list as
// the three list and rating families only.
var allRemoteKinds = []string{"favorite", "watchlist", "rating", "watched", "progress"}

// ListRemoteState passes Pastime's favourites, watchlist, ratings, watched plays
// and resume points to Silo, with Pastime's explicit removals. Every page says
// complete_snapshot=false, so Silo infers no removal from an absent item; only a
// removal Pastime names reaches Silo. A favourite, watchlist or rating removal is
// a tombstone keyed by the item's "catalog:" key: Silo removes only items this
// connection synced, when the connection's removal settings allow it, and a
// rating removal loses to a rating changed in Silo since they last agreed. A
// watched removal is a movie or show the profile deleted from its Pastime
// library, which Silo applies to that profile's watch history up to the
// deletion time, keeping later watches. Silo adds matched titles and re-keys
// items it imported under external IDs to Pastime's "catalog:" keys. Removals
// also flow from Silo to Pastime. Pastime never returns plays or resume points
// it received from Silo, and Silo keeps an imported watch only when it is newer
// than its own.
func (p *Provider) ListRemoteState(ctx context.Context, r *v1.WatchSyncListRemoteStateRequest) (*v1.WatchSyncListRemoteStateResponse, error) {
	out := &v1.WatchSyncListRemoteStateResponse{}
	if r.GetPageSize() < 0 || r.GetPageSize() > maxBatch || len(r.GetStateKinds()) > len(remoteKinds) {
		out.Fault = invalidRequest("Silo asked for a read Pastime does not offer.")
		return out, nil
	}
	read := client.PersonalRead{Cursor: r.GetCursor(), PageToken: r.GetPageToken(), PageSize: int(r.GetPageSize())}
	for _, kind := range r.GetStateKinds() {
		name, ok := remoteKinds[kind]
		if !ok {
			out.Fault = invalidRequest("Silo asked for a read Pastime does not offer.")
			return out, nil
		}
		read.Kinds = append(read.Kinds, name)
	}
	if len(read.Kinds) == 0 {
		read.Kinds = slices.Clone(allRemoteKinds)
	}
	s, f := p.open(ctx, r.GetContext(), true)
	if f != nil {
		out.Fault = f
		return out, nil
	}
	page, err := p.backend.ReadPersonalState(ctx, s.origin, s.key, read)
	if err != nil {
		out.Fault = syncFault(err)
		return out, nil
	}
	for _, item := range page.Items {
		if !slices.Contains(read.Kinds, item.Kind) {
			return &v1.WatchSyncListRemoteStateResponse{Fault: temporary(msgUnreadable, 0)}, nil
		}
		if item.Removed {
			state, ok := watchedRemoval(item)
			if item.Kind != "watched" {
				state, ok = listRemoval(item)
			}
			if !ok {
				return &v1.WatchSyncListRemoteStateResponse{Fault: temporary(msgUnreadable, 0)}, nil
			}
			out.Items = append(out.Items, state)
			continue
		}
		state, ok := remoteState(item)
		if !ok {
			return &v1.WatchSyncListRemoteStateResponse{Fault: temporary(msgUnreadable, 0)}, nil
		}
		out.Items = append(out.Items, state)
	}
	out.CompleteSnapshot = false
	out.NextPageToken = page.NextPageToken
	out.NextCursor = page.NextCursor
	out.Warnings = page.Warnings
	return out, nil
}

// validIDs checks a map of stable video identifiers: between min and three of
// tmdb, imdb and tvdb, each non-empty and at most 80 characters.
func validIDs(ids map[string]string, min int) bool {
	if len(ids) < min || len(ids) > 3 {
		return false
	}
	for ns, value := range ids {
		if !slices.Contains(idNamespaces, ns) || value == "" || len(value) > 80 {
			return false
		}
	}
	return true
}

// remoteState checks one present item against Pastime's closed shape. Lists
// and ratings name movies and series; watched plays and resume points name
// movies and episodes, never a whole series.
func remoteState(item client.PersonalRemoteItem) (*v1.WatchSyncRemoteState, bool) {
	id, ok := strings.CutPrefix(item.ProviderItemKey, "catalog:")
	if !ok || len(item.ProviderItemKey) > 64 || !positiveDecimal(id) || item.ChangedAt.IsZero() {
		return nil, false
	}
	video := item.Kind == "watched" || item.Kind == "progress"
	episodeFields := item.SeriesExternalIDs != nil || item.SeasonNumber != nil || item.EpisodeNumber != nil
	media := &v1.WatchSyncMedia{ExternalIds: item.ExternalIDs}
	switch item.MediaKind {
	case "movie", "series":
		if episodeFields || !validIDs(item.ExternalIDs, 1) || video && item.MediaKind == "series" {
			return nil, false
		}
		media.MediaType = v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE
		if item.MediaKind == "series" {
			media.MediaType = v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES
		}
	case "episode":
		if !video || !validIDs(item.ExternalIDs, 0) || !validIDs(item.SeriesExternalIDs, 1) ||
			item.SeasonNumber == nil || item.EpisodeNumber == nil || *item.SeasonNumber < 0 || *item.EpisodeNumber < 0 {
			return nil, false
		}
		media.MediaType = v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_EPISODE
		media.SeriesExternalIds = item.SeriesExternalIDs
		media.SeasonNumber, media.EpisodeNumber = *item.SeasonNumber, *item.EpisodeNumber
	default:
		return nil, false
	}
	at := timestamppb.New(item.ChangedAt)
	if at.CheckValid() != nil {
		return nil, false
	}
	if video && (item.Rating != nil || item.Position != nil) || !video && (item.PlayCount != nil || item.ProgressPercent != nil) {
		return nil, false
	}
	out := &v1.WatchSyncRemoteState{ProviderItemKey: item.ProviderItemKey, Media: media}
	switch item.Kind {
	case "favorite":
		if item.Rating != nil || item.Position != nil {
			return nil, false
		}
		out.Favorite = &v1.WatchSyncRemoteListState{ListedAt: at}
	case "watchlist":
		if item.Rating != nil || item.Position != nil && *item.Position < 0 {
			return nil, false
		}
		out.Watchlist = &v1.WatchSyncRemoteListState{ListedAt: at}
	case "rating":
		if item.Position != nil || item.Rating == nil || *item.Rating < 2 || *item.Rating > 10 || *item.Rating%2 != 0 {
			return nil, false
		}
		out.Rating = &v1.WatchSyncRemoteRatingState{Rating: int32(*item.Rating), RatedAt: at}
	case "watched":
		if item.ProgressPercent != nil || item.PlayCount == nil || *item.PlayCount < 1 || *item.PlayCount > math.MaxInt32 {
			return nil, false
		}
		out.Watched = &v1.WatchSyncRemoteWatchedState{PlayCount: int32(*item.PlayCount), LastWatchedAt: at}
	case "progress":
		if item.PlayCount != nil || item.ProgressPercent == nil || math.IsNaN(*item.ProgressPercent) || *item.ProgressPercent < 0 || *item.ProgressPercent >= 100 {
			return nil, false
		}
		out.Progress = &v1.WatchSyncRemoteProgressState{ProgressPercent: *item.ProgressPercent, PausedAt: at}
	default:
		return nil, false
	}
	return out, true
}

// watchedRemoval checks one watched removal: a movie or show, keyed
// "removed:catalog:<id>", with its deletion time and nothing else, and passes it
// as a watched state with removed=true and the time in last_watched_at.
func watchedRemoval(item client.PersonalRemoteItem) (*v1.WatchSyncRemoteState, bool) {
	id, ok := strings.CutPrefix(item.ProviderItemKey, "removed:catalog:")
	if !ok || len(item.ProviderItemKey) > 64 || !positiveDecimal(id) || item.ChangedAt.IsZero() ||
		item.SeriesExternalIDs != nil || item.SeasonNumber != nil || item.EpisodeNumber != nil ||
		item.PlayCount != nil || item.ProgressPercent != nil || item.Rating != nil || item.Position != nil ||
		!validIDs(item.ExternalIDs, 1) {
		return nil, false
	}
	media := &v1.WatchSyncMedia{ExternalIds: item.ExternalIDs}
	switch item.MediaKind {
	case "movie":
		media.MediaType = v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE
	case "series":
		media.MediaType = v1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_SERIES
	default:
		return nil, false
	}
	at := timestamppb.New(item.ChangedAt)
	if at.CheckValid() != nil {
		return nil, false
	}
	return &v1.WatchSyncRemoteState{ProviderItemKey: item.ProviderItemKey, Media: media,
		Watched: &v1.WatchSyncRemoteWatchedState{LastWatchedAt: at, Removed: true}}, true
}

// listRemoval checks one favourite, watchlist or rating removal: a "catalog:"
// key and its time. Silo resolves it by that key alone, so no media is sent.
func listRemoval(item client.PersonalRemoteItem) (*v1.WatchSyncRemoteState, bool) {
	id, ok := strings.CutPrefix(item.ProviderItemKey, "catalog:")
	if !ok || len(item.ProviderItemKey) > 64 || !positiveDecimal(id) || item.ChangedAt.IsZero() ||
		item.PlayCount != nil || item.ProgressPercent != nil {
		return nil, false
	}
	at := timestamppb.New(item.ChangedAt)
	if at.CheckValid() != nil {
		return nil, false
	}
	out := &v1.WatchSyncRemoteState{ProviderItemKey: item.ProviderItemKey}
	switch item.Kind {
	case "favorite":
		out.Favorite = &v1.WatchSyncRemoteListState{ListedAt: at, Removed: true}
	case "watchlist":
		out.Watchlist = &v1.WatchSyncRemoteListState{ListedAt: at, Removed: true}
	case "rating":
		out.Rating = &v1.WatchSyncRemoteRatingState{RatedAt: at, Removed: true}
	default:
		return nil, false
	}
	return out, true
}
