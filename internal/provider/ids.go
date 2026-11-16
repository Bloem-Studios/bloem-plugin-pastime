package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// The IDs below reproduce, byte for byte, the ones Bloem's former
// managed-tracking host derived for Pastime (bloem-server
// internal/managedtracking/events.go:28-32,241-245 and personal_state.go:101,
// removed by the Pastime back-out). Pastime keys recorded plays, playback
// entity state and personal state on them, so any change here would record a
// replayed play a second time. ids_test.go pins them against a verbatim copy
// of the legacy function and fixed vectors.

// stableTuple hashes the JSON array of parts with encoding/json's defaults:
// no spaces and HTML escaping on. Keep it identical to the legacy function.
func stableTuple(prefix string, parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return prefix + hex.EncodeToString(h[:])
}

// playbackEntityID names the playback state of one Silo media item (a movie
// or an episode).
func playbackEntityID(mediaItemID string) string {
	return stableTuple("media_", mediaItemID)
}

// personalEntityID names one personal-state lane (favorite, watchlist or
// rating) of one Silo media item.
func personalEntityID(lane, mediaItemID string) string {
	return stableTuple("state_", lane, mediaItemID)
}

// consumptionID names one completed viewing. accountID and profileID are the
// Silo account and profile the Pastime key is bound to, and watchHistoryID is
// the Silo watch-history row, so a live stop, a history export and any retry
// of the same viewing share it and Pastime records the play once.
func consumptionID(accountID, profileID, watchHistoryID string) string {
	return stableTuple("play_", accountID, profileID, watchHistoryID)
}

// revision orders events on one entity by when they happened, in Unix
// microseconds. Every value after 1970 exceeds the host sequences the legacy
// integration stored (at most 17,056 live), so no new event is judged stale
// against them. It returns zero, which Pastime rejects, for times at or
// before the Unix epoch.
func revision(at time.Time) uint64 {
	us := at.UnixMicro()
	if us < 1 {
		return 0
	}
	return uint64(us)
}

// wireEventID derives the event ID sent to Pastime from the exact payload,
// whose own event_id must be empty. Pastime refuses a known event ID with a
// different payload, and Silo reuses its event IDs for different payloads
// (list events, resumed sessions), so the payload decides the ID: an exact
// retry repeats it and any other change gets a new one. The "pevt_" prefix
// cannot collide with the legacy host's "bloem_evt_" receipts.
func wireEventID(payload any) string {
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return "pevt_" + hex.EncodeToString(h[:])
}
