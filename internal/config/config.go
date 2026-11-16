// Package config reads the Pastime address from the installation settings
// Silo sends with every watch-sync call.
package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"

	v1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const (
	// Key is the manifest's global_config_schema key.
	Key = "pastime"
	// Field is the address field inside that schema.
	Field = "pastime_url"
	// ValueKey is where Silo puts the field in WatchSyncProviderConfig.values:
	// it flattens global settings as <config key>.<field>.
	ValueKey = Key + "." + Field
	// LoopbackEnv, set to "1" in the plugin process environment, also allows
	// http:// for a loopback host. It exists for isolated test deployments and
	// is never a settings field.
	LoopbackEnv = "BLOEM_PASTIME_ALLOW_LOOPBACK_HTTP"
)

var (
	// ErrMissing means the administrator has not set the Pastime address.
	ErrMissing = errors.New("pastime address not set")
	// ErrInvalid means the saved address is not an acceptable origin.
	ErrInvalid = errors.New("pastime address invalid")
)

// Origin returns the Pastime origin, without a trailing slash, from the
// settings Silo sent with a call.
func Origin(c *v1.WatchSyncProviderConfig) (string, error) {
	return Parse(c.GetValues()[ValueKey], os.Getenv(LoopbackEnv) == "1")
}

// Parse accepts an https origin with a host name, no user information, query,
// fragment or path other than "/". With allowLoopbackHTTP it also accepts
// http:// for localhost or a loopback IP.
func Parse(raw string, allowLoopbackHTTP bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrMissing
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 255 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return "", ErrInvalid
	}
	local := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		local = ip.IsLoopback()
	}
	loopbackHTTP := allowLoopbackHTTP && local && u.Scheme == "http"
	if u.Scheme != "https" && !loopbackHTTP {
		return "", ErrInvalid
	}
	return u.Scheme + "://" + u.Host, nil
}
