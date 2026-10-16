package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"

	"github.com/kaanemec/vde/internal/config"
)

// The fetch errors the JSON path maps to responses; others mean the fetch failed.
var (
	errFetchRefused  = errors.New("api: document url refused")
	errFetchTooLarge = errors.New("api: fetched document too large")
	errFetchAddress  = errors.New("api: document url resolves to a non-public address")
)

// fetcher gets JSON submissions' document urls (documents.fetch), against SSRF: HTTPS
// only, hosts on the allow-list only, redirects only to the same host, and every
// connection checked at dial time, after DNS resolution, against loopback, private,
// link-local and other non-public addresses (so a rebinding name gains nothing). No
// proxy from the environment is used, since a proxy would hide the address.
type fetcher struct {
	hosts    map[string]bool // lower-case host or host:port
	maxBytes int64           // 0: the document limit alone
	client   *http.Client
}

// newFetcher returns the fetcher of c. tlsConfig and allow are nil in production;
// tests pass a test server's roots and allow loopback.
func newFetcher(c config.Fetch, tlsConfig *tls.Config, allow func(netip.Addr) bool) *fetcher {
	if allow == nil {
		allow = publicAddr
	}
	dialer := &net.Dialer{Timeout: c.Timeout, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !allow(ap.Addr().Unmap()) {
			return errFetchAddress
		}
		return nil
	}}
	f := &fetcher{hosts: map[string]bool{}, maxBytes: c.MaxBytes}
	for _, h := range c.AllowedHosts {
		f.hosts[strings.ToLower(h)] = true
	}
	f.client = &http.Client{
		Timeout: c.Timeout,
		Transport: &http.Transport{
			DialContext: dialer.DialContext, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: c.Timeout,
			ForceAttemptHTTP2: true, MaxIdleConns: 4, IdleConnTimeout: c.Timeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				return errFetchRefused
			}
			return nil
		},
	}
	return f
}

// nonPublic are ranges IsGlobalUnicast and IsPrivate leave out: shared address space,
// IETF protocol assignments, benchmarking, and NAT64, which can embed a private IPv4.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

// publicAddr reports whether a is a public unicast address.
func publicAddr(a netip.Addr) bool {
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return false // also loopback, link-local, multicast, unspecified
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// allowed reports whether u's host:port, or its host on the default port, is listed.
func (f *fetcher) allowed(u *url.URL) bool {
	return f.hosts[strings.ToLower(u.Host)] || u.Port() == "" && f.hosts[strings.ToLower(u.Hostname())]
}

// get fetches rawURL, at most min(max_bytes, limit) bytes.
func (f *fetcher) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil || !f.allowed(u) {
		return nil, errFetchRefused
	}
	if f.maxBytes > 0 {
		limit = min(limit, f.maxBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errFetchRefused
	}
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, errFetchRefused) {
			return nil, errFetchRefused
		}
		return nil, fmt.Errorf("api: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api: fetch: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("api: fetch: %w", err)
	case int64(len(data)) > limit:
		return nil, errFetchTooLarge
	}
	return data, nil
}
