package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/bloem-plugin-pastime/internal/client"
	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const (
	cTemporary   = v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_TEMPORARY
	cRateLimited = v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_RATE_LIMITED
	cCredential  = v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_CREDENTIAL
	cDenied      = v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMISSION_DENIED
	cInvalid     = v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_INVALID_REQUEST
	cPermanent   = v1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMANENT

	sApplied  = v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED
	sNoChange = v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_NO_CHANGE
	sRetry    = v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY
	sRejected = v1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_REJECTED
)

// The contract's fault table (replacement contract section 4.2): what each
// Pastime answer becomes during a sync, and whether it concerns one event or
// the whole request. Connection-scoped answers on an event write make Silo
// ignore the batch's results; none of them is PERMISSION_DENIED, which would
// mark a history batch failed and spend an attempt.
func TestFaultTable(t *testing.T) {
	type want struct {
		connection bool
		status     v1.WatchSyncApplyStatus
		code       v1.WatchSyncFaultCode
		message    string
		retryAfter time.Duration
	}
	for _, c := range []struct {
		name string
		err  error
		want want
	}{
		{"401", &client.Fault{Status: 401, Code: "unauthenticated"}, want{connection: true, code: cCredential, message: msgReconnect}},
		{"bad stored key", &client.Fault{Code: client.CodeInvalidKey}, want{connection: true, code: cCredential, message: msgReconnect}},
		{"403 service access", &client.Fault{Status: 403, Code: "service_access_required"}, want{connection: true, code: cTemporary, message: msgNoAccess}},
		{"403 binding", &client.Fault{Status: 403, Code: "forbidden"}, want{connection: true, code: cCredential, message: msgOtherProfile}},
		{"410", &client.Fault{Status: 410, Code: "gone"}, want{connection: true, code: cTemporary, message: msgGone}},
		{"404 no_match", &client.Fault{Status: 404, Code: "no_match"}, want{status: sRejected, code: cPermanent, message: msgNoMatch}},
		{"404 not_found", &client.Fault{Status: 404, Code: "not_found"}, want{status: sRejected, code: cPermanent, message: msgNoMatch}},
		{"404 no code", &client.Fault{Status: 404}, want{connection: true, code: cTemporary, message: msgNotPastime}},
		// Pastime release A answers a route it does not have with unknown_route:
		// a wrong address or version, never a title Pastime refused.
		{"404 unknown route", &client.Fault{Status: 404, Code: "unknown_route"}, want{connection: true, code: cTemporary, message: msgNotPastime}},
		{"405", &client.Fault{Status: 405}, want{connection: true, code: cTemporary, message: msgNotPastime}},
		{"409 conflict", &client.Fault{Status: 409, Code: "conflict"}, want{status: sRejected, code: cPermanent, message: msgConflict}},
		{"409 rating", &client.Fault{Status: 409, Code: "rating_precision"}, want{status: sRejected, code: cPermanent, message: msgRatingPrecision}},
		{"413", &client.Fault{Status: 413}, want{status: sRejected, code: cInvalid, message: msgInvalid}},
		{"415", &client.Fault{Status: 415, Code: "unsupported_media_type"}, want{status: sRejected, code: cInvalid, message: msgInvalid}},
		{"422", &client.Fault{Status: 422, Code: "validation_error"}, want{status: sRejected, code: cInvalid, message: msgInvalid}},
		{"429", &client.Fault{Status: 429, Code: "rate_limited", RetryAfter: 60 * time.Second}, want{status: sRetry, code: cRateLimited, message: msgRateLimited, retryAfter: time.Minute}},
		{"429 no header", &client.Fault{Status: 429}, want{status: sRetry, code: cRateLimited, message: msgRateLimited, retryAfter: defaultRateLimitWait}},
		{"503 catalog_loading", &client.Fault{Status: 503, Code: "catalog_loading", RetryAfter: time.Minute}, want{status: sRetry, code: cTemporary, message: msgCatalog, retryAfter: time.Minute}},
		{"503 catalog_pending", &client.Fault{Status: 503, Code: "catalog_pending", RetryAfter: 5 * time.Minute}, want{status: sRetry, code: cTemporary, message: msgCatalog, retryAfter: 5 * time.Minute}},
		{"503 other", &client.Fault{Status: 503, Code: "provider_unavailable", RetryAfter: time.Second}, want{connection: true, code: cTemporary, message: msgUnavailable, retryAfter: time.Second}},
		{"500", &client.Fault{Status: 500, Code: "internal_error"}, want{connection: true, code: cTemporary, message: msgUnavailable}},
		{"502", &client.Fault{Status: 502}, want{connection: true, code: cTemporary, message: msgUnavailable}},
		{"504", &client.Fault{Status: 504}, want{connection: true, code: cTemporary, message: msgUnavailable}},
		{"302", &client.Fault{Status: 302}, want{connection: true, code: cTemporary, message: msgUnavailable}},
		{"transport", &client.Fault{Code: client.CodeTransport}, want{connection: true, code: cTemporary, message: msgUnavailable}},
		{"deadline", context.DeadlineExceeded, want{connection: true, code: cTemporary, message: msgUnavailable}},
		{"closed schema", &client.Fault{Code: client.CodeInvalidResponse}, want{connection: true, code: cTemporary, message: msgUnreadable}},
		{"unknown error", errors.New("boom"), want{connection: true, code: cTemporary, message: msgUnreadable}},
	} {
		status, f := eventOutcome(c.err)
		if f == nil {
			if !c.want.connection {
				t.Errorf("%s: became a connection fault", c.name)
				continue
			}
			f = syncFault(c.err)
		} else if c.want.connection || status != c.want.status {
			t.Errorf("%s: event %v", c.name, status)
			continue
		}
		if f.Code != c.want.code || f.SafeMessage != c.want.message || f.GetRetryAfter().AsDuration() != c.want.retryAfter {
			t.Errorf("%s: %v %q %v", c.name, f.Code, f.SafeMessage, f.GetRetryAfter().AsDuration())
		}
	}
}

// Outside event writes every failure concerns the whole request.
func TestSyncFaultsForReads(t *testing.T) {
	for _, c := range []struct {
		err  error
		code v1.WatchSyncFaultCode
	}{
		{&client.Fault{Status: 409, Code: "rating_precision"}, cPermanent},
		{&client.Fault{Status: 422, Code: "validation_error"}, cTemporary},
		{&client.Fault{Status: 429}, cRateLimited},
		{&client.Fault{Status: 503, Code: "catalog_loading"}, cTemporary},
		{&client.Fault{Status: 404, Code: "no_match"}, cTemporary},
		{&client.Fault{Status: 403, Code: "service_access_required"}, cTemporary},
		{&client.Fault{Status: 410}, cTemporary},
	} {
		if f := syncFault(c.err); f.Code != c.code {
			t.Errorf("%v: %v", c.err, f.Code)
		}
	}
}

// Connecting (ExchangeAPIKey, GetAccount) reports access problems truthfully:
// no export is involved, and the person sees why the key does not work.
func TestConnectFaultsReportPermissionDenied(t *testing.T) {
	for _, c := range []struct {
		err     error
		message string
	}{
		{&client.Fault{Status: 403, Code: "service_access_required"}, msgNoAccess},
		{&client.Fault{Status: 403, Code: "forbidden"}, msgOtherProfile},
		{&client.Fault{Status: 410, Code: "gone"}, msgGone},
	} {
		if f := connectionFault(c.err); f.Code != cDenied || f.SafeMessage != c.message {
			t.Errorf("%v: %v %q", c.err, f.Code, f.SafeMessage)
		}
	}
}

// Only Pastime's own 500 may belong to one event; everything else concerns
// the service.
func TestOnlyInternalErrorsAreEventScoped(t *testing.T) {
	for err, want := range map[error]bool{
		&client.Fault{Status: 500, Code: "internal_error"}:       true,
		&client.Fault{Status: 500}:                               true,
		&client.Fault{Status: 502}:                               false,
		&client.Fault{Status: 503, Code: "provider_unavailable"}: false,
		&client.Fault{Status: 504}:                               false,
		&client.Fault{Code: client.CodeTransport}:                false,
		&client.Fault{Code: client.CodeInvalidResponse}:          false,
		context.DeadlineExceeded:                                 false,
	} {
		if got := eventInternalError(err); got != want {
			t.Errorf("%v: %v", err, got)
		}
	}
}

func TestSafeMessagesNameSiloAndNoSecrets(t *testing.T) {
	for _, m := range []string{msgReconnect, msgNotAKey, msgKeyRejected, msgRefresh, msgKeyOnly, msgNoAccess, msgOtherProfile, msgGone, msgNoMatch, msgConflict, msgRatingPrecision, msgInvalid, msgRateLimited, msgCatalog, msgUnavailable, msgNoAddress, msgBadAddress, msgNotPastime, msgUnreadable, msgCapability, msgEventFailed, msgNextRun} {
		if strings.Contains(m, "Bloem") || strings.Contains(m, "managed") || strings.Contains(m, "ptb") || len(m) > 200 {
			t.Errorf("message %q", m)
		}
	}
}
