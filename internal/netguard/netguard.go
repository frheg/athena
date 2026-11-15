// Package netguard is an HTTP client that only talks to the public internet.
//
// A tool that fetches URLs on the model's behalf must not become a door into the private network (databases, the
// cluster API, router pages, cloud metadata). The check runs on the IP address the dialer actually connects to, so a
// name that resolves to a public address first and a private one later (DNS rebinding) cannot slip through, and it
// runs again for every redirect.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is returned for a destination outside the public internet or outside the allowed hosts.
var ErrBlocked = errors.New("destination not allowed")

var blocked = mustNets(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
	"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
)

func mustNets(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// Public reports whether ip is a public unicast address.
func Public(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blocked {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// Options for a guarded client.
type Options struct {
	Timeout      time.Duration
	MaxRedirects int
	// AllowHosts, when set, limits requests to these host names (exact, or a suffix starting with ".").
	AllowHosts []string
	// AllowPrivate turns the address check off. Only for tests against a local server.
	AllowPrivate bool
	UserAgent    string
}

// Client returns an http.Client that refuses private destinations and non-http(s) schemes.
func Client(o Options) *http.Client {
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = 5
	}
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if o.AllowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if !Public(net.ParseIP(host)) {
				return fmt.Errorf("%w: %s is not a public address", ErrBlocked, host)
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil, // never through a proxy: the proxy would make the connection, not us
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: o.Timeout,
	}
	c := &http.Client{Timeout: o.Timeout, Transport: &uaTransport{next: tr, ua: o.UserAgent, o: o}}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= o.MaxRedirects {
			return fmt.Errorf("more than %d redirects", o.MaxRedirects)
		}
		return CheckURL(req.URL.Scheme, req.URL.Hostname(), o)
	}
	return c
}

// CheckURL validates the scheme and, if AllowHosts is set, the host.
func CheckURL(scheme, host string, o Options) error {
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrBlocked, scheme)
	}
	if len(o.AllowHosts) == 0 {
		return nil
	}
	host = strings.ToLower(host)
	for _, a := range o.AllowHosts {
		a = strings.ToLower(a)
		if host == a || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a)) {
			return nil
		}
	}
	return fmt.Errorf("%w: host %s", ErrBlocked, host)
}

type uaTransport struct {
	next http.RoundTripper
	ua   string
	o    Options
}

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := CheckURL(r.URL.Scheme, r.URL.Hostname(), t.o); err != nil {
		return nil, err
	}
	if t.ua != "" && r.Header.Get("User-Agent") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", t.ua)
	}
	return t.next.RoundTrip(r)
}

// ReadLimited reads at most max bytes and reports whether the body was longer.
func ReadLimited(r io.Reader, max int64) ([]byte, bool, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > max {
		return b[:max], true, nil
	}
	return b, false, nil
}

// GetJSON is a small helper for API tools: GET url, fail on non-2xx, return the body (at most max bytes).
func GetJSON(ctx context.Context, c *http.Client, url string, header map[string]string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _, err := ReadLimited(res.Body, max)
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s answered %d: %s", req.URL.Host, res.StatusCode, strings.TrimSpace(string(b[:min(len(b), 200)])))
	}
	return b, nil
}
