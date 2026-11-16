package client

import (
	"context"
	"time"
)

// PersonalRead is the body of POST /state:read.
type PersonalRead struct {
	Kinds     []string `json:"kinds"`
	Cursor    string   `json:"cursor,omitempty"`
	PageToken string   `json:"page_token,omitempty"`
	PageSize  int      `json:"page_size"`
}

// PersonalRemoteItem is one personal state in Pastime: a favourite, watchlist
// entry or rating of a movie or series, or the watched plays or resume point of
// a movie or episode recorded outside Silo. An episode names its show through
// SeriesExternalIDs and its place by season and episode number. ChangedAt is the
// last play of a watched item and the time of a resume point.
type PersonalRemoteItem struct {
	ProviderItemKey   string            `json:"provider_item_key"`
	Kind              string            `json:"kind"`
	MediaKind         string            `json:"media_kind"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SeriesExternalIDs map[string]string `json:"series_external_ids,omitempty"`
	SeasonNumber      *int32            `json:"season_number,omitempty"`
	EpisodeNumber     *int32            `json:"episode_number,omitempty"`
	Removed           bool              `json:"removed"`
	Rating            *int              `json:"rating,omitempty"`
	Position          *int32            `json:"position,omitempty"`
	PlayCount         *int              `json:"play_count,omitempty"`
	ProgressPercent   *float64          `json:"progress_percent,omitempty"`
	ChangedAt         time.Time         `json:"changed_at"`
}

// PersonalPage is one page of a personal-state read.
type PersonalPage struct {
	Items            []PersonalRemoteItem `json:"items"`
	NextPageToken    string               `json:"next_page_token"`
	NextCursor       string               `json:"next_cursor"`
	CompleteSnapshot bool                 `json:"complete_snapshot"`
	Warnings         []string             `json:"warnings"`
}

// ReadPersonalState reads one page. A page that breaks the paging rules is an
// invalid response: a final page names cursor "full-v1", any other page a
// continuation token, never both.
func (c *Client) ReadPersonalState(ctx context.Context, origin, key string, r PersonalRead) (PersonalPage, error) {
	var out PersonalPage
	if err := c.post(ctx, origin, "/state:read", key, r, &out); err != nil {
		return out, err
	}
	limit := r.PageSize
	if limit == 0 {
		limit = 100
	}
	if len(out.Items) > limit || len(out.NextPageToken) > 128 || out.NextPageToken != "" && out.NextCursor != "" || out.NextPageToken == "" && out.NextCursor != "full-v1" || len(out.Warnings) > 10 {
		return out, &Fault{Code: CodeInvalidResponse}
	}
	return out, nil
}
