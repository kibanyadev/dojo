package webpush

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
)

// DefaultHosts are the push services browsers use (design 3.10).
var DefaultHosts = []string{"fcm.googleapis.com", "updates.push.services.mozilla.com", "web.push.apple.com", "*.notify.windows.com"}

// ErrEndpoint marks an endpoint or address the policy refuses.
var ErrEndpoint = errors.New("webpush: endpoint not allowed")

// Policy decides which endpoints may be contacted. Origins (scheme://host[:port]) are exempt from the
// https, port and host rules and from the dial guard; they exist for the local fake-push and are
// accepted only for localhost, a loopback address or the compose name fake-push.
type Policy struct {
	hosts   []string
	origins []string
}

// NewPolicy builds a Policy; it fails on an origin that is not a local stand-in.
func NewPolicy(hosts, origins []string) (Policy, error) {
	p := Policy{hosts: hosts}
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Host == "" ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return Policy{}, fmt.Errorf("webpush: bad push origin %q", o)
		}
		if !localHost(strings.ToLower(u.Hostname())) {
			return Policy{}, fmt.Errorf("webpush: push origin %q is not localhost, loopback or fake-push", o)
		}
		p.origins = append(p.origins, u.Scheme+"://"+strings.ToLower(u.Host))
	}
	return p, nil
}

func localHost(h string) bool {
	if h == "localhost" || h == "fake-push" {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.IsLoopback()
}

func (p Policy) isOrigin(u *url.URL) bool {
	if u.User != nil {
		return false
	}
	o := u.Scheme + "://" + strings.ToLower(u.Host)
	for _, x := range p.origins {
		if x == o {
			return true
		}
	}
	return false
}

// Check validates an endpoint URL; the error wraps ErrEndpoint.
func (p Policy) Check(endpoint string) error {
	_, err := p.parse(endpoint)
	return err
}

// Normalize validates an endpoint like Check and returns it with its host normalized, the form to store
// so two spellings of one endpoint are one row; the error wraps ErrEndpoint.
func (p Policy) Normalize(endpoint string) (string, error) {
	u, err := p.parse(endpoint)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// parse returns the endpoint with its host normalized (lower case, no trailing dot, no :443).
func (p Policy) parse(endpoint string) (*url.URL, error) {
	if len(endpoint) > 1024 {
		return nil, fmt.Errorf("%w: too long", ErrEndpoint)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: unparsable", ErrEndpoint)
	}
	if p.isOrigin(u) {
		u.Host = strings.ToLower(u.Host)
		return u, nil
	}
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("%w: need https on 443 without userinfo", ErrEndpoint)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, fmt.Errorf("%w: IP literal", ErrEndpoint)
	}
	if !p.hostAllowed(host) {
		return nil, fmt.Errorf("%w: host", ErrEndpoint)
	}
	u.Host = host
	return u, nil
}

func (p Policy) hostAllowed(host string) bool {
	if host == "" || strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return false
	}
	for _, h := range p.hosts {
		h = strings.TrimSuffix(strings.ToLower(h), ".")
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

func prefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

var (
	blockedNets = prefixes("100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4", "255.255.255.255/32", "64:ff9b:1::/48", "::/96", "fec0::/10")
	nat64       = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour   = netip.MustParsePrefix("2002::/16")
)

// blocked reports whether ip is not a public unicast address. A NAT64 (64:ff9b::/96) or 6to4
// (2002::/16) address is judged by the IPv4 address it embeds.
func blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	b := ip.As16()
	switch {
	case ip.Is6() && nat64.Contains(ip):
		return blocked(netip.AddrFrom4([4]byte(b[12:16])))
	case ip.Is6() && sixToFour.Contains(ip):
		return blocked(netip.AddrFrom4([4]byte(b[2:6])))
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// guard is a net.Dialer Control hook: it checks the resolved address as the connection is made.
func guard(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if blocked(ip) {
		return fmt.Errorf("%w: address %s", ErrEndpoint, ip)
	}
	return nil
}

// Topic is the RFC 8030 Topic for a kind and ref: 32 characters of the base64url sha256.
func Topic(kind, ref string) string {
	sum := sha256.Sum256([]byte(kind + ":" + ref))
	return enc.EncodeToString(sum[:])[:32]
}
