package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

// legacyStableTuple is a verbatim copy of stableTuple from bloem-server
// internal/managedtracking/events.go:28-32 at f1e6f1ab5, the host code that
// derived every ID Pastime stores today. Do not edit it: it is the reference
// the plugin's own derivation must match.
func legacyStableTuple(prefix string, parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return prefix + hex.EncodeToString(h[:])
}

// Golden inputs and outputs from the replacement contract (section 3.2),
// computed with the legacy function under Go 1.26.9. A live check matched the
// same formulas against 25 of 25 recent production receipts.
const (
	goldenAccount = "7"
	goldenProfile = "3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71"
	goldenHistory = "0b5e7c1a-9d2f-4e6b-8a3c-5f1d2e3c4b5a"
	goldenMedia   = "c0ffee00-0000-4000-8000-000000000001"

	goldenPlayJSON  = `["7","3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71","0b5e7c1a-9d2f-4e6b-8a3c-5f1d2e3c4b5a"]`
	goldenPlay      = "play_d5d9186f6e2ab5b22309a289e2c222c15c090ac946666d33e8bc7b0b42a6bad5"
	goldenMediaID   = "media_12b4c378fd9832438f7d0b655f7d632dddd31ab6d4dcb37fcabea34932b4eba7"
	goldenFavorite  = "state_e4914f1cce6da1640bec4ceb3fef3e22bf54a0449f2360a7dc9f52a1b4ebb621"
	goldenWatchlist = "state_1464f3d8119b4ac570d30b57ab0acf87aa6d9e19b9a6f3edcc1a4f61fbed1fff"
	goldenRating    = "state_4419c43a983eefcf0d9250fadba67bb6abf6ba53f7680e7a3e4f5fe42b479263"
	goldenEscaped   = "play_bc364ee6bf44f9798358ddebb2cdf0d4789ac18c68f28617969fa843b1ce389c"
	goldenRevision  = uint64(1791981296789123)
)

func TestIDsMatchLegacyGoldenVectors(t *testing.T) {
	b, err := json.Marshal([]string{goldenAccount, goldenProfile, goldenHistory})
	if err != nil || string(b) != goldenPlayJSON {
		t.Fatalf("tuple JSON = %s, %v", b, err)
	}
	for name, c := range map[string]struct{ got, legacy, want string }{
		"play":      {consumptionID(goldenAccount, goldenProfile, goldenHistory), legacyStableTuple("play_", goldenAccount, goldenProfile, goldenHistory), goldenPlay},
		"media":     {playbackEntityID(goldenMedia), legacyStableTuple("media_", goldenMedia), goldenMediaID},
		"favorite":  {personalEntityID("favorite", goldenMedia), legacyStableTuple("state_", "favorite", goldenMedia), goldenFavorite},
		"watchlist": {personalEntityID("watchlist", goldenMedia), legacyStableTuple("state_", "watchlist", goldenMedia), goldenWatchlist},
		"rating":    {personalEntityID("rating", goldenMedia), legacyStableTuple("state_", "rating", goldenMedia), goldenRating},
		"escaped":   {stableTuple("play_", "a<b&c>"), legacyStableTuple("play_", "a<b&c>"), goldenEscaped},
	} {
		if c.got != c.want || c.legacy != c.want {
			t.Errorf("%s: plugin %s, legacy %s, want %s", name, c.got, c.legacy, c.want)
		}
	}
}

func TestStableTupleKeepsDefaultHTMLEscaping(t *testing.T) {
	b, _ := json.Marshal([]string{"a<b&c>"})
	bs := "\\"
	if want := `["a` + bs + `u003cb` + bs + `u0026c` + bs + `u003e"]`; string(b) != want {
		t.Fatalf("encoding/json no longer escapes HTML by default: %s", b)
	}
}

func TestStableTupleMatchesLegacyForAwkwardInputs(t *testing.T) {
	inputs := [][]string{
		{},
		{""},
		{"", ""},
		{`quote"backslash\`},
		{"line separator", "tab\tnew\nline"},
		{"ünïcödé", "😀", "\x00\x1f"},
		{"invalid\xffutf8"},
		{"42", "e4bcbddc5", "1e9b9c4a-2c1f-4d8e-9f00-aa00bb11cc22"},
	}
	for _, parts := range inputs {
		for _, prefix := range []string{"media_", "state_", "play_"} {
			if got, want := stableTuple(prefix, parts...), legacyStableTuple(prefix, parts...); got != want {
				t.Errorf("%q %q: %s != %s", prefix, parts, got, want)
			}
		}
	}
}

func TestRevisionIsUnixMicroseconds(t *testing.T) {
	at := time.Date(2026, 10, 14, 12, 34, 56, 789123456, time.UTC)
	if got := revision(at); got != goldenRevision {
		t.Fatalf("revision = %d, want %d", got, goldenRevision)
	}
	if got := revision(at.In(time.FixedZone("x", 7200))); got != goldenRevision {
		t.Fatalf("revision depends on the zone: %d", got)
	}
	if revision(time.Unix(0, 0)) != 0 || revision(time.Unix(0, 999)) != 0 || revision(time.Unix(-1, 0)) != 0 {
		t.Fatal("revision at or before the epoch must be zero")
	}
	if revision(time.Unix(0, 1000)) != 1 {
		t.Fatal("first microsecond after the epoch must be revision 1")
	}
	// Every new revision exceeds the largest stored legacy host sequence.
	if revision(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) <= 17056 {
		t.Fatal("new revisions must exceed legacy host sequences")
	}
}

func TestWireEventIDIsPayloadDigest(t *testing.T) {
	type payload struct {
		EventID string `json:"event_id"`
		Value   int    `json:"value"`
	}
	a := wireEventID(payload{Value: 1})
	if a != wireEventID(payload{Value: 1}) {
		t.Fatal("same payload must give the same ID")
	}
	if a == wireEventID(payload{Value: 2}) {
		t.Fatal("different payloads must give different IDs")
	}
	sum := sha256.Sum256([]byte(`{"event_id":"","value":1}`))
	if a != "pevt_"+hex.EncodeToString(sum[:]) || len(a) != 69 {
		t.Fatalf("wire ID %s", a)
	}
	if wireEventID(make(chan int)) != "" {
		t.Fatal("an unencodable payload must give no ID")
	}
}
