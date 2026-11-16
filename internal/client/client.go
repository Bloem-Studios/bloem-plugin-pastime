// Package client calls Pastime's Silo sync routes with one profile's key.
// Requests and errors never carry the key, response bodies or messages.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// basePath prefixes every sync route. Pastime keeps the "bloem" segment
	// for compatibility with plugin 0.1.14.
	basePath = "/api/v1/integrations/bloem"
	// maxResponse bounds a success body; maxProblem bounds an error body read
	// only for its code.
	maxResponse = 1 << 20
	maxProblem  = 4 << 10
	// maxRetryAfter caps a Retry-After hint.
	maxRetryAfter = 5 * time.Minute
)

// Local fault codes for failures without a Pastime answer.
const (
	CodeTransport       = "transport_unavailable"
	CodeInvalidResponse = "invalid_response"
	CodeInvalidKey      = "invalid_key"
	CodeInvalidRequest  = "invalid_request"
)

// Fault describes a failed call without request URLs, bodies, messages or
// credentials. Status is the HTTP status, or zero when Pastime gave no usable
// answer. Code is Pastime's machine code when it sent one in
// [a-z0-9_]{1,64}, otherwise empty, or one of the local codes above.
type Fault struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (f *Fault) Error() string {
	return "pastime: status " + strconv.Itoa(f.Status) + " " + f.Code
}

// Client is safe for concurrent use. Each call names the Pastime origin, so
// a settings change applies to the next call.
type Client struct {
	http      *http.Client
	userAgent string
}

func New(version string) *Client {
	return &Client{
		http: &http.Client{
			Timeout: 20 * time.Second,
			// A redirect could carry the key elsewhere; treat it as a failure.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		userAgent: "bloem-pastime/" + version,
	}
}

// ValidKey reports whether key looks like a Pastime sync key.
func ValidKey(key string) bool {
	if !strings.HasPrefix(key, "ptbs_") || len(key) <= 5 || len(key) > 256 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c <= ' ' || c >= 0x7f {
			return false
		}
	}
	return true
}

func (c *Client) post(ctx context.Context, origin, path, key string, in, out any) error {
	if !ValidKey(key) {
		return &Fault{Code: CodeInvalidKey}
	}
	body, err := json.Marshal(in)
	if err != nil {
		return &Fault{Code: CodeInvalidRequest}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+basePath+path, bytes.NewReader(body))
	if err != nil {
		return &Fault{Code: CodeInvalidRequest}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Fault{Code: CodeTransport}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &Fault{Status: resp.StatusCode, Code: problemCode(resp.Body), RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(b) > maxResponse {
		return &Fault{Code: CodeInvalidResponse}
	}
	// The response schema is closed: an unknown field means Pastime and the
	// plugin disagree about the contract, so nothing in it is trusted.
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(out); err != nil {
		return &Fault{Code: CodeInvalidResponse}
	}
	if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &Fault{Code: CodeInvalidResponse}
	}
	return nil
}

// problemCode reads the "code" of a bounded JSON error body and ignores the
// rest. An oversized or malformed body, or an unexpected code, yields "".
func problemCode(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, maxProblem+1))
	if err != nil || len(b) > maxProblem {
		return ""
	}
	var problem struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(b, &problem) != nil || len(problem.Code) == 0 || len(problem.Code) > 64 {
		return ""
	}
	for i := 0; i < len(problem.Code); i++ {
		if c := problem.Code[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return ""
		}
	}
	return problem.Code
}

// retryAfter reads delay-seconds or an HTTP date, capped at five minutes.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return min(time.Duration(min(n, 300))*time.Second, maxRetryAfter)
	}
	if at, err := http.ParseTime(v); err == nil {
		return min(max(time.Until(at), 0), maxRetryAfter)
	}
	return 0
}

// Inspect returns the Silo account and profile a key is bound to.
func (c *Client) Inspect(ctx context.Context, origin, key string) (Binding, error) {
	var out Binding
	err := c.post(ctx, origin, "/credentials:inspect", key, struct{}{}, &out)
	if err == nil && (out.InstanceID == "" || out.AccountID == "" || out.ProfileID == "" || out.Name == "") {
		err = &Fault{Code: CodeInvalidResponse}
	}
	return out, err
}

// ApplyPlayback sends one playback event.
func (c *Client) ApplyPlayback(ctx context.Context, origin, key string, e PlaybackEvent) (EventResult, error) {
	return c.apply(ctx, origin, "/events:apply", key, e)
}

// ApplyPersonal sends one favourite, watchlist or rating change.
func (c *Client) ApplyPersonal(ctx context.Context, origin, key string, e PersonalEvent) (EventResult, error) {
	return c.apply(ctx, origin, "/state/events:apply", key, e)
}

func (c *Client) apply(ctx context.Context, origin, path, key string, in any) (EventResult, error) {
	var out EventResult
	err := c.post(ctx, origin, path, key, in, &out)
	if err == nil && !slices.Contains([]string{"applied", "stale", "no_change"}, out.Status) {
		err = &Fault{Code: CodeInvalidResponse}
	}
	return out, err
}
