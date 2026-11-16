package provider

import (
	"context"
	"errors"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/config"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Safe messages. Silo stores and shows them; they never carry keys, IDs,
// addresses or Pastime's own messages.
const (
	msgReconnect       = "Pastime no longer accepts this key. Create a new key on Pastime's Connect Silo page and reconnect."
	msgNotAKey         = "This is not a Pastime key. Create one on Pastime's Connect Silo page."
	msgKeyRejected     = "This Pastime key is not valid or has expired. Create a new key on Pastime's Connect Silo page."
	msgRefresh         = "Create a new key on Pastime's Connect Silo page and reconnect."
	msgKeyOnly         = "Pastime connects with a key."
	msgNoAccess        = "Pastime has not given this account access to tracking. Ask the Pastime administrator."
	msgOtherProfile    = "This Pastime key belongs to another profile. Reconnect with a new key."
	msgGone            = "Pastime has turned off Silo sync."
	msgNoMatch         = "Pastime could not match this title."
	msgConflict        = "Pastime already holds a different version of this change."
	msgRatingPrecision = "A Pastime rating has no exact Silo equivalent. Change it explicitly in Pastime before synchronizing ratings."
	msgInvalid         = "Pastime rejected this change as invalid."
	msgRateLimited     = "Pastime is rate limiting this connection."
	msgCatalog         = "Pastime is still loading this title. Silo will retry."
	msgUnavailable     = "Pastime is temporarily unavailable."
	msgNoAddress       = "Set the Pastime address in the Pastime plugin settings."
	msgBadAddress      = "The Pastime address in the Pastime plugin settings must be an https address with no path."
	msgNotPastime      = "Pastime did not answer at the configured address. Check the Pastime address in the plugin settings."
	msgUnreadable      = "Pastime sent data Silo cannot read. Silo will try again."
	msgCapability      = "This request is not for the Pastime capability."
	msgEventFailed     = "Pastime could not apply this change. Silo will retry it."
	msgNextRun         = "Silo will send the rest of this batch on its next run."
)

// defaultRateLimitWait applies when a 429 carries no Retry-After: Silo would
// otherwise defer the connection for an hour, while Pastime's limiter window
// is one minute.
const defaultRateLimitWait = time.Minute

func newFault(code v1.WatchSyncFaultCode, message string) *v1.WatchSyncFault {
	return &v1.WatchSyncFault{Code: code, SafeMessage: message}
}

func invalidRequest(message string) *v1.WatchSyncFault {
	return newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_REQUEST, message)
}

func temporary(message string, retryAfter time.Duration) *v1.WatchSyncFault {
	f := newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_TEMPORARY, message)
	if retryAfter > 0 {
		f.RetryAfter = durationpb.New(retryAfter)
	}
	return f
}

func rateLimited(retryAfter time.Duration) *v1.WatchSyncFault {
	if retryAfter <= 0 {
		retryAfter = defaultRateLimitWait
	}
	f := newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_RATE_LIMITED, msgRateLimited)
	f.RetryAfter = durationpb.New(retryAfter)
	return f
}

// addressFault explains why the configured Pastime address cannot be used.
// It is TEMPORARY so Silo keeps every pending change and spends no retry
// attempt until an administrator fixes the setting.
func addressFault(err error) *v1.WatchSyncFault {
	if errors.Is(err, config.ErrMissing) {
		return temporary(msgNoAddress, 0)
	}
	return temporary(msgBadAddress, 0)
}

func asFault(err error) *client.Fault {
	var f *client.Fault
	if errors.As(err, &f) {
		return f
	}
	// Cancellation and deadlines: Pastime gave no answer.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &client.Fault{Code: client.CodeTransport}
	}
	return &client.Fault{Code: client.CodeInvalidResponse}
}

// syncFault maps a failed call during a sync (ApplyEvents, ListRemoteState) to
// a response-level fault. Silo marks every play of a history batch failed and
// spends one of its five attempts on PERMISSION_DENIED, so answers that say
// nothing about the plays themselves never use it here:
//
//   - Missing tracking access (403 service_access_required) and Silo sync turned
//     off (410) are TEMPORARY: Silo marks nothing, keeps the plays pending and
//     shows the message as the run error, and the backlog goes out once
//     Pastime accepts the key again. Pastime denies access to everyone except
//     administrators while its access feed is late, so this must not strand
//     exports.
//   - A key bound to another profile (403 forbidden) is INVALID_CREDENTIAL:
//     Silo saves the message on the connection, marks nothing, and the person
//     reconnects.
func syncFault(err error) *v1.WatchSyncFault {
	f := asFault(err)
	switch {
	case f.Status == 403 && f.Code == "service_access_required":
		return temporary(msgNoAccess, 0)
	case f.Status == 403:
		return newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, msgOtherProfile)
	case f.Status == 410:
		return temporary(msgGone, 0)
	}
	return connectionFault(err)
}

// connectionFault maps a failed call to a response-level fault, which Silo
// applies to the whole request. Connecting (ExchangeAPIKey, GetAccount) uses
// it directly and reports access problems as PERMISSION_DENIED; syncs go
// through syncFault.
func connectionFault(err error) *v1.WatchSyncFault {
	f := asFault(err)
	switch {
	case f.Status == 401, f.Status == 0 && f.Code == client.CodeInvalidKey:
		return newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL, msgReconnect)
	case f.Status == 403 && f.Code == "service_access_required":
		return newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMISSION_DENIED, msgNoAccess)
	case f.Status == 403:
		return newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMISSION_DENIED, msgOtherProfile)
	case f.Status == 410:
		return newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMISSION_DENIED, msgGone)
	case f.Status == 429:
		return rateLimited(f.RetryAfter)
	case f.Status == 409 && f.Code == "rating_precision":
		return newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMANENT, msgRatingPrecision)
	case f.Status == 404, f.Status == 405:
		return temporary(msgNotPastime, 0)
	case f.Status == 0 && f.Code == client.CodeInvalidResponse, f.Status == 422:
		// A broken contract, or a read traversal Pastime no longer holds:
		// the next run starts again.
		return temporary(msgUnreadable, 0)
	default:
		// 5xx, transport failures and anything unexpected. Nothing is marked,
		// so Silo spends no retry attempt; re-sending is safe because wire IDs
		// are payload digests and completions dedupe by consumption ID.
		return temporary(msgUnavailable, f.RetryAfter)
	}
}

// eventInternalError reports whether an event write failed with Pastime's
// 500: an unexpected error that may belong to this one event. ApplyEvents then
// checks that Pastime is up before deciding between a per-event retry and a
// batch fault. Gateway errors, other 503s, transport failures and unreadable
// answers concern the service and stay batch faults.
func eventInternalError(err error) bool {
	f := asFault(err)
	return f.Status == 500
}

// deferred returns the rest of a batch to Silo without an attempt: per-event
// RETRY with RATE_LIMITED keeps the plays pending and resumes after a second.
func deferred() *v1.WatchSyncFault {
	f := rateLimited(time.Second)
	f.SafeMessage = msgNextRun
	return f
}

// eventOutcome maps a failed event write. A nil fault means the failure
// concerns the whole connection: the caller returns syncFault(err) and Silo
// ignores every per-event result of the request.
func eventOutcome(err error) (v1.WatchSyncApplyStatus, *v1.WatchSyncFault) {
	f := asFault(err)
	rejected := v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_REJECTED
	retry := v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY
	switch {
	case f.Status == 404 && (f.Code == "no_match" || f.Code == "not_found"):
		return rejected, newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMANENT, msgNoMatch)
	case f.Status == 409 && f.Code == "rating_precision":
		return rejected, newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMANENT, msgRatingPrecision)
	case f.Status == 409:
		return rejected, newFault(v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMANENT, msgConflict)
	case f.Status == 413, f.Status == 415, f.Status == 422:
		return rejected, invalidRequest(msgInvalid)
	case f.Status == 429:
		return retry, rateLimited(f.RetryAfter)
	case f.Status == 503 && (f.Code == "catalog_loading" || f.Code == "catalog_pending"):
		return retry, temporary(msgCatalog, f.RetryAfter)
	default:
		return v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_UNSPECIFIED, nil
	}
}
