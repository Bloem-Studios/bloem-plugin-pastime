// Package provider implements Pastime's watch_sync_provider.v1 capability: an
// ordinary API-key provider that answers Silo's calls and keeps no state.
package provider

import (
	"context"
	"strings"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/config"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const (
	capabilityID = "pastime"
	// maxBatch matches the manifest's max_batch_size.
	maxBatch = 100
	// requestReserve is the time a Pastime request needs before Silo's deadline
	// for a watch-sync call (two minutes): the client's 20-second timeout plus
	// a margin for the answer to reach Silo.
	requestReserve = 25 * time.Second
)

// Backend is Pastime's sync API, implemented by client.Client.
type Backend interface {
	Inspect(ctx context.Context, origin, key string) (client.Binding, error)
	ApplyPlayback(ctx context.Context, origin, key string, e client.PlaybackEvent) (client.EventResult, error)
	ApplyPersonal(ctx context.Context, origin, key string, e client.PersonalEvent) (client.EventResult, error)
	ReadPersonalState(ctx context.Context, origin, key string, r client.PersonalRead) (client.PersonalPage, error)
}

type Provider struct {
	v1.UnimplementedWatchSyncProviderServer
	backend Backend
	now     func() time.Time
	// reserve is requestReserve; tests shorten it.
	reserve time.Duration
}

func New(b Backend) *Provider {
	return &Provider{backend: b, now: time.Now, reserve: requestReserve}
}

// timeLeft reports whether another Pastime request fits before ctx's
// deadline. Without a deadline there is always time.
func (p *Provider) timeLeft(ctx context.Context) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) >= p.reserve
}

// session is one authenticated call: where Pastime is, the profile's key, and
// what Pastime says the key is bound to.
type session struct {
	origin, key string
	binding     client.Binding
}

// open checks a connection's stored credentials against Pastime. The secret
// attributes saved at connect time must still match what Pastime reports for
// the key; connections enrolled by the former managed integration carry the
// same three attributes, so both kinds are checked the same way. During a
// sync (forSync) faults never spend Silo's export attempts: see syncFault.
func (p *Provider) open(ctx context.Context, a *v1.WatchSyncAuthenticatedContext, forSync bool) (session, *v1.WatchSyncFault) {
	if a.GetCapabilityId() != capabilityID {
		return session{}, invalidRequest(msgCapability)
	}
	origin, err := config.Origin(a.GetProviderConfig())
	if err != nil {
		return session{}, addressFault(err)
	}
	key := a.GetCredentials().GetAccessToken()
	if !client.ValidKey(key) {
		return session{}, newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, msgReconnect)
	}
	b, err := p.backend.Inspect(ctx, origin, key)
	if err != nil {
		if forSync {
			return session{}, syncFault(err)
		}
		return session{}, connectionFault(err)
	}
	if !sameBinding(a.GetCredentials().GetSecretAttributes(), b) {
		if forSync {
			// The key now names another profile: reconnecting fixes it, and
			// INVALID_CREDENTIAL marks no export failed meanwhile.
			return session{}, newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, msgOtherProfile)
		}
		return session{}, newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMISSION_DENIED, msgOtherProfile)
	}
	return session{origin: origin, key: key, binding: b}, nil
}

func sameBinding(attrs map[string]string, b client.Binding) bool {
	return attrs["instance_id"] == b.InstanceID && attrs["account_id"] == b.AccountID && attrs["profile_id"] == b.ProfileID
}

func account(b client.Binding) *v1.WatchSyncAccount {
	return &v1.WatchSyncAccount{ExternalSubject: b.ProfileID, Username: b.Name, DisplayName: b.Name}
}

func (p *Provider) GetAccount(ctx context.Context, r *v1.WatchSyncGetAccountRequest) (*v1.WatchSyncGetAccountResponse, error) {
	s, f := p.open(ctx, r.GetContext(), false)
	if f != nil {
		return &v1.WatchSyncGetAccountResponse{Fault: f}, nil
	}
	return &v1.WatchSyncGetAccountResponse{Account: account(s.binding)}, nil
}

// ExchangeAPIKey connects a profile with a key from Pastime's Connect Silo
// page. The stored credential has the same shape as the ones the former
// managed integration wrote: the key plus the instance, account and profile
// IDs Pastime binds it to. It has no expiry and no refresh token: Pastime
// extends a key's expiry while it is used, and Silo only refreshes
// credentials that carry an expiry.
func (p *Provider) ExchangeAPIKey(ctx context.Context, r *v1.WatchSyncExchangeAPIKeyRequest) (*v1.WatchSyncCredentialResponse, error) {
	out := &v1.WatchSyncCredentialResponse{}
	if r.GetCapabilityId() != capabilityID {
		out.Fault = invalidRequest(msgCapability)
		return out, nil
	}
	origin, err := config.Origin(r.GetProviderConfig())
	if err != nil {
		out.Fault = addressFault(err)
		return out, nil
	}
	key := strings.TrimSpace(r.GetApiKey())
	if !client.ValidKey(key) {
		out.Fault = newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, msgNotAKey)
		return out, nil
	}
	b, err := p.backend.Inspect(ctx, origin, key)
	if err != nil {
		out.Fault = connectionFault(err)
		if out.Fault.Code == v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL {
			out.Fault.SafeMessage = msgKeyRejected
		}
		return out, nil
	}
	out.Credentials = &v1.WatchSyncCredentials{
		AccessToken: key,
		SecretAttributes: map[string]string{
			"instance_id": b.InstanceID,
			"account_id":  b.AccountID,
			"profile_id":  b.ProfileID,
		},
	}
	out.Account = account(b)
	return out, nil
}

// RefreshCredentials is never needed: stored credentials have no expiry, so
// Silo does not call it. A connection that does reach it reconnects.
func (p *Provider) RefreshCredentials(context.Context, *v1.WatchSyncRefreshCredentialsRequest) (*v1.WatchSyncCredentialResponse, error) {
	return &v1.WatchSyncCredentialResponse{Fault: newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, msgRefresh)}, nil
}

func (p *Provider) InitAuthorize(context.Context, *v1.WatchSyncInitAuthorizeRequest) (*v1.WatchSyncInitAuthorizeResponse, error) {
	return &v1.WatchSyncInitAuthorizeResponse{Fault: invalidRequest(msgKeyOnly)}, nil
}

func (p *Provider) ExchangeCode(context.Context, *v1.WatchSyncExchangeCodeRequest) (*v1.WatchSyncCredentialResponse, error) {
	return &v1.WatchSyncCredentialResponse{Fault: invalidRequest(msgKeyOnly)}, nil
}

// ApplyEvents sends each event to Pastime in order and reports a result per
// event under Silo's own event ID.
//
//   - A rejected or untranslatable event is REJECTED and the rest continue.
//   - A title Pastime is still loading is RETRY/TEMPORARY and the rest continue.
//   - A Pastime 500 for one event is RETRY/TEMPORARY for that event when
//     Pastime still answers credentials:inspect, so one event Pastime cannot
//     apply uses Silo's attempts instead of holding the batch; otherwise it is
//     a batch fault.
//   - A rate limit makes this and every later event RETRY/RATE_LIMITED, which
//     Silo keeps pending without spending an attempt. Events that no longer
//     fit before Silo's deadline are returned the same way, so the results so
//     far reach Silo.
//   - A key, access or service failure becomes a response-level fault: Silo
//     then ignores every result, marks nothing and spends no attempt.
func (p *Provider) ApplyEvents(ctx context.Context, r *v1.WatchSyncApplyEventsRequest) (*v1.WatchSyncApplyEventsResponse, error) {
	if n := len(r.GetEvents()); n == 0 || n > maxBatch {
		return &v1.WatchSyncApplyEventsResponse{Fault: invalidRequest("Send between 1 and 100 events.")}, nil
	}
	s, f := p.open(ctx, r.GetContext(), true)
	if f != nil {
		return &v1.WatchSyncApplyEventsResponse{Fault: f}, nil
	}
	now := p.now().UTC().Truncate(time.Microsecond)
	out := &v1.WatchSyncApplyEventsResponse{}
	var limited *v1.WatchSyncFault
	for _, event := range r.GetEvents() {
		result := &v1.WatchSyncApplyResult{EventId: event.GetEventId()}
		out.Results = append(out.Results, result)
		if limited == nil && !p.timeLeft(ctx) {
			limited = deferred()
		}
		if limited != nil {
			result.Status, result.Fault = v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY, limited
			continue
		}
		status, rejected, err := p.apply(ctx, s, event, now)
		switch {
		case rejected != nil:
			result.Status, result.Fault = v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_REJECTED, rejected
		case err == nil:
			result.Status = status
		default:
			status, f := eventOutcome(err)
			if f == nil && eventInternalError(err) {
				status, f = p.afterInternalError(ctx, s)
			}
			if f == nil {
				return &v1.WatchSyncApplyEventsResponse{Fault: syncFault(err)}, nil
			}
			result.Status, result.Fault = status, f
			if f.Code == v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_RATE_LIMITED {
				limited = f
			}
		}
	}
	return out, nil
}

// afterInternalError decides what a Pastime 500 on one event means. When
// Pastime still answers credentials:inspect for the same binding, the event is
// RETRY/TEMPORARY on its own. When there is no time left to ask, the event and
// the rest are deferred. A nil fault means the failure concerns the whole
// request.
func (p *Provider) afterInternalError(ctx context.Context, s session) (v1.WatchSyncApplyStatus, *v1.WatchSyncFault) {
	if !p.timeLeft(ctx) {
		return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY, deferred()
	}
	b, err := p.backend.Inspect(ctx, s.origin, s.key)
	if err != nil || b != s.binding {
		return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_UNSPECIFIED, nil
	}
	return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY, temporary(msgEventFailed, 0)
}

// apply translates and sends one event. It returns a fault without calling
// Pastime when the event cannot be represented, and Pastime's error otherwise.
func (p *Provider) apply(ctx context.Context, s session, e *v1.WatchSyncEvent, now time.Time) (v1.WatchSyncApplyStatus, *v1.WatchSyncFault, error) {
	if personalLane(e.GetOperation()) != "" {
		payload, f := personalEvent(s.binding, e, now)
		if f != nil {
			return 0, f, nil
		}
		result, err := p.backend.ApplyPersonal(ctx, s.origin, s.key, payload)
		if err != nil {
			return 0, nil, err
		}
		if result.Status == "applied" {
			return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED, nil, nil
		}
		return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE, nil, nil
	}
	payload, f := playbackEvent(s.binding, e, now)
	if f != nil {
		return 0, f, nil
	}
	result, err := p.backend.ApplyPlayback(ctx, s.origin, s.key, payload)
	if err != nil {
		return 0, nil, err
	}
	// A completion Pastime already counted (a replay of a delivered play) is
	// NO_CHANGE, as is any change older than what Pastime holds.
	if result.Status != "applied" || payload.Completed && !result.PlayRecorded {
		return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE, nil, nil
	}
	return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED, nil, nil
}
