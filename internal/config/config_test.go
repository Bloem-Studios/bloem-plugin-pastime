package config

import (
	"errors"
	"testing"

	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestParse(t *testing.T) {
	for raw, want := range map[string]string{
		"https://pastime.example.com":        "https://pastime.example.com",
		"https://pastime.example.com/":       "https://pastime.example.com",
		" https://pastime.example.com:8443 ": "https://pastime.example.com:8443",
		"HTTPS://Pastime.Example.com":        "https://Pastime.Example.com",
	} {
		if got, err := Parse(raw, false); err != nil || got != want {
			t.Errorf("%q: %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"http://pastime.example.com",
		"https://pastime.example.com/pastime",
		"https://user:pass@pastime.example.com",
		"https://pastime.example.com?x=1",
		"https://pastime.example.com/?",
		"https://pastime.example.com#top",
		"https://",
		"pastime.example.com",
		"ftp://pastime.example.com",
		"http://127.0.0.1:18081",
		"https://" + string(make([]byte, 300)),
	} {
		if _, err := Parse(raw, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted: %v", raw, err)
		}
	}
	if _, err := Parse("  ", false); !errors.Is(err, ErrMissing) {
		t.Fatalf("blank address: %v", err)
	}
}

func TestLoopbackHTTPNeedsTheOverride(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:18081", "http://localhost:18081/", "http://[::1]:18081"} {
		if _, err := Parse(raw, true); err != nil {
			t.Errorf("%q refused with the override: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://pastime.example.com", "http://192.0.2.1", "http://localhost.example.com"} {
		if _, err := Parse(raw, true); err == nil {
			t.Errorf("%q accepted as loopback", raw)
		}
	}
}

func TestOriginReadsTheFlattenedInstallationSetting(t *testing.T) {
	t.Setenv(LoopbackEnv, "")
	c := &v1.WatchSyncProviderConfig{Values: map[string]string{"pastime.pastime_url": "https://pastime.example.com/"}}
	if got, err := Origin(c); err != nil || got != "https://pastime.example.com" {
		t.Fatalf("%q, %v", got, err)
	}
	if _, err := Origin(nil); !errors.Is(err, ErrMissing) {
		t.Fatalf("no settings: %v", err)
	}
	// Only the declared public value counts.
	secret := &v1.WatchSyncProviderConfig{SecretValues: map[string]string{"pastime.pastime_url": "https://pastime.example.com"}}
	if _, err := Origin(secret); !errors.Is(err, ErrMissing) {
		t.Fatalf("secret value used: %v", err)
	}
	loopback := &v1.WatchSyncProviderConfig{Values: map[string]string{"pastime.pastime_url": "http://127.0.0.1:18081"}}
	if _, err := Origin(loopback); !errors.Is(err, ErrInvalid) {
		t.Fatalf("loopback http without the override: %v", err)
	}
	t.Setenv(LoopbackEnv, "1")
	if got, err := Origin(loopback); err != nil || got != "http://127.0.0.1:18081" {
		t.Fatalf("loopback http with the override: %q, %v", got, err)
	}
}
