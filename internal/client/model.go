package client

import "time"

// Binding is what a key is bound to: Pastime's instance and the Silo account
// and profile IDs it recorded for the profile, plus the Pastime profile name.
type Binding struct {
	InstanceID string `json:"instance_id"`
	AccountID  string `json:"account_id"`
	ProfileID  string `json:"profile_id"`
	Name       string `json:"name"`
}

// PlaybackMedia identifies a movie or an episode by external IDs. An episode
// also carries its title and its show's title, which Pastime uses to check the
// identifiers and numbers, to find an episode another catalog numbers
// differently, and to find a show none of whose identifiers it knows.
type PlaybackMedia struct {
	Kind              string            `json:"kind"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SeriesExternalIDs map[string]string `json:"series_external_ids,omitempty"`
	Season            *int32            `json:"season,omitempty"`
	Episode           *int32            `json:"episode,omitempty"`
	Title             string            `json:"title,omitempty"`
	SeriesTitle       string            `json:"series_title,omitempty"`
}

// PlaybackEvent is the body of POST /events:apply.
type PlaybackEvent struct {
	Version         int           `json:"version"`
	InstanceID      string        `json:"instance_id"`
	AccountID       string        `json:"account_id"`
	ProfileID       string        `json:"profile_id"`
	EntityID        string        `json:"entity_id"`
	EventID         string        `json:"event_id"`
	Revision        uint64        `json:"revision"`
	Operation       string        `json:"operation"`
	OccurredAt      time.Time     `json:"occurred_at"`
	Completed       bool          `json:"completed"`
	ConsumptionID   string        `json:"consumption_id,omitempty"`
	PositionSeconds int           `json:"position_seconds"`
	DurationSeconds int           `json:"duration_seconds"`
	Media           PlaybackMedia `json:"media"`
}

// PersonalMedia identifies a movie or a series by external IDs or, for a
// title Pastime itself reported, by its Pastime catalog ID (a decimal string;
// Pastime release A accepts it when external_ids is empty).
type PersonalMedia struct {
	Kind          string            `json:"kind"`
	ExternalIDs   map[string]string `json:"external_ids"`
	CatalogItemID string            `json:"catalog_item_id,omitempty"`
}

// PersonalEvent is the body of POST /state/events:apply. The plugin never
// sends a watchlist position: Silo derives it from the batch, so it changes
// between runs and would turn retries into conflicts.
type PersonalEvent struct {
	Version    int           `json:"version"`
	InstanceID string        `json:"instance_id"`
	AccountID  string        `json:"account_id"`
	ProfileID  string        `json:"profile_id"`
	EntityID   string        `json:"entity_id"`
	EventID    string        `json:"event_id"`
	Revision   uint64        `json:"revision"`
	Operation  string        `json:"operation"`
	OccurredAt time.Time     `json:"occurred_at"`
	Rating     *int          `json:"rating,omitempty"`
	Media      PersonalMedia `json:"media"`
}

// EventResult is Pastime's answer to one event.
type EventResult struct {
	Status       string `json:"status"`
	PlayRecorded bool   `json:"play_recorded"`
}
