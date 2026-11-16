package provider

import (
	"context"
	"slices"
	"testing"

	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/proto"
)

func exchange(t *testing.T, f *fakePastime, key string) *v1.WatchSyncCredentialResponse {
	t.Helper()
	out, err := f.provider().ExchangeAPIKey(context.Background(), &v1.WatchSyncExchangeAPIKeyRequest{CapabilityId: "pastime", ProviderConfig: f.config(), ApiKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if out.Fault != nil && out.Credentials != nil {
		t.Fatal("credentials returned with a fault")
	}
	return out
}

// A key from Pastime's Connect Silo page produces the same credential shape
// as the 164 connections the former managed integration enrolled: the key and
// the instance, account and profile attributes, with no expiry, refresh
// token, type or scopes, so Silo never tries to refresh it.
func TestExchangeAPIKeyStoresTheLegacyCredentialShape(t *testing.T) {
	f := newFake(t)
	out := exchange(t, f, "  "+testKey+"\n")
	want := &v1.WatchSyncCredentials{AccessToken: testKey, SecretAttributes: map[string]string{"instance_id": "instance-1", "account_id": goldenAccount, "profile_id": goldenProfile}}
	if out.Fault != nil || !proto.Equal(out.Credentials, want) {
		t.Fatalf("%v", out)
	}
	if a := out.Account; a.ExternalSubject != goldenProfile || a.Username != "Robin" || a.DisplayName != "Robin" {
		t.Fatalf("account %v", a)
	}
	// The stored credential then drives every call, exactly like a legacy row.
	legacy := f.auth()
	if !proto.Equal(legacy.Credentials, out.Credentials) {
		t.Fatalf("legacy and new credentials differ: %v %v", legacy.Credentials, out.Credentials)
	}
	if r := apply(t, f, stop("a", goldenHistory)); !slices.Equal(statuses(r), []v1.WatchSyncApplyStatus{sApplied}) {
		t.Fatalf("%v", r)
	}
}

func TestExchangeAPIKeyFaults(t *testing.T) {
	for _, key := range []string{"", "ptbp_provisioning", "ptbs_", "ptbs_a b", "Bearer ptbs_x"} {
		f := newFake(t)
		if out := exchange(t, f, key); out.Fault.GetCode() != cCredential || out.Fault.GetSafeMessage() != msgNotAKey || f.count() != 0 {
			t.Errorf("%q: %v", key, out)
		}
	}
	for name, c := range map[string]struct {
		answer answer
		code   v1.WatchSyncFaultCode
		msg    string
	}{
		"expired":        {answer{401, "", `{"code":"unauthenticated"}`}, cCredential, msgKeyRejected},
		"service access": {answer{403, "", `{"code":"service_access_required"}`}, cDenied, msgNoAccess},
		"down":           {answer{503, "", `{"code":"unavailable"}`}, cTemporary, msgUnavailable},
	} {
		f := newFake(t)
		f.queue("/credentials:inspect", c.answer)
		if out := exchange(t, f, testKey); out.Fault.GetCode() != c.code || out.Fault.GetSafeMessage() != c.msg {
			t.Errorf("%s: %v", name, out)
		}
	}
	f := newFake(t)
	out, _ := f.provider().ExchangeAPIKey(context.Background(), &v1.WatchSyncExchangeAPIKeyRequest{CapabilityId: "pastime", ApiKey: testKey})
	if out.Fault.GetCode() != cTemporary || out.Fault.GetSafeMessage() != msgNoAddress || f.count() != 0 {
		t.Fatalf("no address: %v", out)
	}
	out, _ = f.provider().ExchangeAPIKey(context.Background(), &v1.WatchSyncExchangeAPIKeyRequest{CapabilityId: "other", ProviderConfig: f.config(), ApiKey: testKey})
	if out.Fault.GetCode() != cInvalid {
		t.Fatalf("capability: %v", out)
	}
}

func TestGetAccount(t *testing.T) {
	f := newFake(t)
	out, _ := f.provider().GetAccount(context.Background(), &v1.WatchSyncGetAccountRequest{Context: f.auth()})
	if out.Fault != nil || out.Account.ExternalSubject != goldenProfile || out.Account.Username != "Robin" {
		t.Fatalf("%v", out)
	}
	f.queue("/credentials:inspect", answer{401, "", `{"code":"unauthenticated"}`})
	out, _ = f.provider().GetAccount(context.Background(), &v1.WatchSyncGetAccountRequest{Context: f.auth()})
	if out.Account != nil || out.Fault.GetCode() != cCredential {
		t.Fatalf("%v", out)
	}
	// Connecting reports access and binding problems as they are.
	f.queue("/credentials:inspect", answer{403, "", `{"code":"service_access_required"}`})
	out, _ = f.provider().GetAccount(context.Background(), &v1.WatchSyncGetAccountRequest{Context: f.auth()})
	if out.Fault.GetCode() != cDenied || out.Fault.GetSafeMessage() != msgNoAccess {
		t.Fatalf("%v", out)
	}
	a := f.auth()
	a.Credentials.SecretAttributes["profile_id"] = "another-profile"
	out, _ = f.provider().GetAccount(context.Background(), &v1.WatchSyncGetAccountRequest{Context: a})
	if out.Fault.GetCode() != cDenied || out.Fault.GetSafeMessage() != msgOtherProfile {
		t.Fatalf("%v", out)
	}
}

// Credentials never expire in Silo; a refresh, should one happen, asks the
// person to reconnect and never returns credentials. Only API keys connect.
func TestNoRefreshAndKeyOnly(t *testing.T) {
	p := New(nil)
	r, _ := p.RefreshCredentials(context.Background(), &v1.WatchSyncRefreshCredentialsRequest{})
	if r.Credentials != nil || r.Fault.GetCode() != cCredential || r.Fault.GetSafeMessage() != msgRefresh {
		t.Fatalf("%v", r)
	}
	i, _ := p.InitAuthorize(context.Background(), &v1.WatchSyncInitAuthorizeRequest{})
	c, _ := p.ExchangeCode(context.Background(), &v1.WatchSyncExchangeCodeRequest{})
	if i.Fault.GetCode() != cInvalid || i.AuthorizationUrl != "" || c.Fault.GetCode() != cInvalid || c.Credentials != nil {
		t.Fatalf("%v %v", i, c)
	}
}
