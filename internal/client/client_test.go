package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const key = "ptbs_synthetic"

func server(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRequestsCarryOnlyTheKeyAndAClosedBody(t *testing.T) {
	origin := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/integrations/bloem/credentials:inspect" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Values("Authorization"); len(got) != 1 || got[0] != "Bearer "+key {
			t.Errorf("authorization %q", got)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "bloem-pastime/9.9.9" {
			t.Errorf("headers %v", r.Header)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Pastime-Profile-ID") != "" {
			t.Error("unexpected identity header")
		}
		if b, _ := io.ReadAll(r.Body); string(b) != "{}" {
			t.Errorf("body %s", b)
		}
		_, _ = fmt.Fprint(w, `{"instance_id":"i","account_id":"7","profile_id":"p","name":"Kid"}`)
	})
	b, err := New("9.9.9").Inspect(context.Background(), origin, key)
	if err != nil || b != (Binding{InstanceID: "i", AccountID: "7", ProfileID: "p", Name: "Kid"}) {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestErrorsKeepStatusCodeAndRetryAfterOnly(t *testing.T) {
	for _, c := range []struct {
		status     int
		body       string
		header     string
		code       string
		retryAfter time.Duration
	}{
		{401, `{"code":"unauthenticated","message":"Sign in ptbs_secret"}`, "", "unauthenticated", 0},
		{403, `{"code":"service_access_required","message":"x"}`, "", "service_access_required", 0},
		{404, `{"code":"no_match","message":"x"}`, "", "no_match", 0},
		{409, `{"code":"rating_precision"}`, "", "rating_precision", 0},
		{422, `{"code":"validation_error"}`, "", "validation_error", 0},
		{429, `{"code":"rate_limited"}`, "60", "rate_limited", time.Minute},
		{429, `{"code":"rate_limited"}`, "86400", "rate_limited", 5 * time.Minute},
		{503, `{"code":"catalog_pending"}`, "21600", "catalog_pending", 5 * time.Minute},
		{503, `{"code":"catalog_loading"}`, "-1", "catalog_loading", 0},
		{500, `private ptbs_secret body`, "", "", 0},
		{502, `{"code":"Not A Code"}`, "", "", 0},
		{503, `{"code":"` + strings.Repeat("a", 65) + `"}`, "", "", 0},
		{503, strings.Repeat(" ", maxProblem) + `{"code":"unavailable"}`, "", "", 0},
		{302, ``, "", "", 0},
	} {
		origin := server(t, func(w http.ResponseWriter, r *http.Request) {
			if c.header != "" {
				w.Header().Set("Retry-After", c.header)
			}
			if c.status == 302 {
				w.Header().Set("Location", "https://elsewhere.example/")
			}
			w.WriteHeader(c.status)
			_, _ = fmt.Fprint(w, c.body)
		})
		_, err := New("0").Inspect(context.Background(), origin, key)
		f, ok := err.(*Fault)
		if !ok || f.Status != c.status || f.Code != c.code || f.RetryAfter != c.retryAfter {
			t.Errorf("%d %s: %#v", c.status, c.body, err)
			continue
		}
		if strings.Contains(f.Error(), "ptbs_") || strings.Contains(f.Error(), "private") {
			t.Errorf("error leaks: %s", f.Error())
		}
	}
}

func TestRetryAfterDate(t *testing.T) {
	if d := retryAfter(time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)); d < 80*time.Second || d > 90*time.Second {
		t.Fatalf("date: %v", d)
	}
	if retryAfter("Wed, 21 Oct 2015 07:28:00 GMT") != 0 || retryAfter("soon") != 0 {
		t.Fatal("past or malformed dates must give no delay")
	}
}

func TestResponsesOutsideTheContractAreInvalid(t *testing.T) {
	for _, body := range []string{
		`{"instance_id":"i","account_id":"7","profile_id":"p","name":"Kid","extra":1}`,
		`{"instance_id":"i","account_id":"7","profile_id":"p","name":"Kid"}{}`,
		`{"instance_id":"i","account_id":"7","profile_id":"p"}`,
		`not json`,
		`{"instance_id":"` + strings.Repeat("x", maxResponse) + `"}`,
	} {
		origin := server(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) })
		_, err := New("0").Inspect(context.Background(), origin, key)
		if f, ok := err.(*Fault); !ok || f.Status != 0 || f.Code != CodeInvalidResponse {
			t.Errorf("%.40s: %v", body, err)
		}
	}
}

func TestApplyAcceptsOnlyKnownStatuses(t *testing.T) {
	var path atomic.Value
	status := "applied"
	origin := server(t, func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		_ = json.NewEncoder(w).Encode(EventResult{Status: status, PlayRecorded: true})
	})
	c := New("0")
	if r, err := c.ApplyPlayback(context.Background(), origin, key, PlaybackEvent{}); err != nil || !r.PlayRecorded || path.Load() != "/api/v1/integrations/bloem/events:apply" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := c.ApplyPersonal(context.Background(), origin, key, PersonalEvent{}); err != nil || path.Load() != "/api/v1/integrations/bloem/state/events:apply" {
		t.Fatal(err)
	}
	status = "done"
	if _, err := c.ApplyPersonal(context.Background(), origin, key, PersonalEvent{}); err == nil {
		t.Fatal("unknown status accepted")
	}
}

func TestInvalidKeysAndUnreachableOriginsNeverSend(t *testing.T) {
	var calls atomic.Int32
	origin := server(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	for _, k := range []string{"", "ptbs_", "ptbp_x", "ptbs_a b", "ptbs_a\n", "ptbs_\x7f", "ptbs_" + strings.Repeat("a", 252)} {
		if _, err := New("0").Inspect(context.Background(), origin, k); err == nil || err.(*Fault).Code != CodeInvalidKey {
			t.Errorf("%q: %v", k, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("an invalid key was sent")
	}
	_, err := New("0").Inspect(context.Background(), "http://127.0.0.1:1", key)
	if f, ok := err.(*Fault); !ok || f.Code != CodeTransport || f.Status != 0 {
		t.Fatalf("transport: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New("0").Inspect(ctx, origin, key); err != context.Canceled {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestReadPersonalStatePaging(t *testing.T) {
	for body, ok := range map[string]bool{
		`{"items":[],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`:      true,
		`{"items":[],"next_page_token":"t1","next_cursor":"","complete_snapshot":true,"warnings":[]}`:           true,
		`{"items":[],"next_page_token":"t1","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`:    false,
		`{"items":[],"next_page_token":"","next_cursor":"","complete_snapshot":true,"warnings":[]}`:             false,
		`{"items":[{},{}],"next_page_token":"","next_cursor":"full-v1","complete_snapshot":true,"warnings":[]}`: false,
	} {
		origin := server(t, func(w http.ResponseWriter, r *http.Request) {
			var in PersonalRead
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.PageSize != 1 || in.Cursor != "full-v1" || in.PageToken != "t0" {
				t.Errorf("request %+v", in)
			}
			_, _ = fmt.Fprint(w, body)
		})
		_, err := New("0").ReadPersonalState(context.Background(), origin, key, PersonalRead{Kinds: []string{"favorite"}, Cursor: "full-v1", PageToken: "t0", PageSize: 1})
		if (err == nil) != ok {
			t.Errorf("%s: %v", body, err)
		}
	}
}
