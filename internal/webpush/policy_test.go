package webpush

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPolicyCheck(t *testing.T) {
	p, err := NewPolicy(DefaultHosts, []string{"http://fake-push:8080"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		endpoint string
		ok       bool
	}{
		{"https://fcm.googleapis.com/fcm/send/abc", true},
		{"https://FCM.googleapis.com./x", true},
		{"https://fcm.googleapis.com:443/x", true},
		{"https://wns2-db5p.notify.windows.com/w/?token=1", true},
		{"https://a.b.notify.windows.com/x", true},
		{"https://notify.windows.com/x", false},
		{"https://evilnotify.windows.com/x", false},
		{"https://.notify.windows.com/x", false},
		{"https://notify.windows.com.evil.example/x", false},
		{"https://fcm.googleapis.com.evil.example/x", false},
		{"https://evil.example/x", false},
		{"http://fcm.googleapis.com/x", false},
		{"https://fcm.googleapis.com:8443/x", false},
		{"https://user:pw@fcm.googleapis.com/x", false},
		{"https://127.0.0.1/x", false},
		{"https://[::1]/x", false},
		{"https://[::ffff:8.8.8.8]/x", false},
		{"https://fcm.googleapis.com/" + strings.Repeat("a", 1024), false},
		{"/relative", false},
		{"http://fake-push:8080/push/t", true},
		{"http://fake-push:9090/push/t", false},
		{"https://fake-push:8080/push/t", false},
		{"http://user@fake-push:8080/push/t", false},
		{"http://FAKE-PUSH:8080/push/t", true},
	}
	for _, tt := range tests {
		err := p.Check(tt.endpoint)
		if (err == nil) != tt.ok {
			t.Errorf("%q: err=%v, want ok=%v", tt.endpoint, err, tt.ok)
		}
		if err != nil && !errors.Is(err, ErrEndpoint) {
			t.Errorf("%q: error does not wrap ErrEndpoint", tt.endpoint)
		}
	}
}

func TestNewPolicyOrigins(t *testing.T) {
	for _, o := range []string{"http://fake-push:8080", "http://localhost:9", "http://127.0.0.1:8082", "http://[::1]:8082", "https://localhost"} {
		if _, err := NewPolicy(nil, []string{o}); err != nil {
			t.Errorf("%s: %v", o, err)
		}
	}
	for _, o := range []string{"http://evil.example", "http://fake-push.evil.example:8080", "http://10.0.0.1", "http://169.254.169.254",
		"http://user@fake-push:8080", "ftp://fake-push", "http://fake-push/path", "fake-push:8080", "", "http://"} {
		if _, err := NewPolicy(nil, []string{o}); err == nil {
			t.Errorf("%q accepted", o)
		}
	}
}

func TestParseNormalizesHost(t *testing.T) {
	p, _ := NewPolicy(DefaultHosts, nil)
	u, err := p.parse("https://FCM.Googleapis.com.:443/fcm/send/a?b=c")
	if err != nil || u.String() != "https://fcm.googleapis.com/fcm/send/a?b=c" {
		t.Fatalf("%v %v", u, err)
	}
}

func TestGuardRefusesNonPublicAddresses(t *testing.T) {
	for _, a := range []string{
		"127.0.0.1:443", "10.0.0.1:443", "172.16.0.1:443", "192.168.1.1:443", "169.254.169.254:443",
		"[::1]:443", "[fe80::1]:443", "[fc00::1]:443", "224.0.0.1:443", "0.0.0.0:443", "[::]:443", "[::ffff:127.0.0.1]:443",
		"100.64.0.1:443", "100.127.255.255:443", "192.0.0.1:443", "198.18.0.1:443", "198.19.255.255:443",
		"240.0.0.1:443", "255.255.255.255:443", "[64:ff9b:1::1]:443", "[64:ff9b:1:ffff::1]:443",
		"[64:ff9b::7f00:1]:443", "[64:ff9b::a00:1]:443", "[64:ff9b::a9fe:a9fe]:443", "[64:ff9b::6440:1]:443",
		"[2002:7f00:1::]:443", "[2002:a00:1::1]:443", "[2002:a9fe:a9fe::]:443", "[2002:c0a8:101::]:443",
		"[::127.0.0.1]:443", "[::a9fe:a9fe]:443", "[fec0::1]:443", "[feff:ffff::1]:443",
	} {
		if err := guard("tcp", a, nil); !errors.Is(err, ErrEndpoint) {
			t.Errorf("%s: %v", a, err)
		}
	}
	for _, a := range []string{"8.8.8.8:443", "[2606:4700::1111]:443", "100.63.255.255:443", "100.128.0.1:443", "198.17.0.1:443",
		"198.20.0.1:443", "[64:ff9b::808:808]:443", "[2002:808:808::]:443"} {
		if err := guard("tcp", a, nil); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
}

func TestGuardedClientRefusesLoopbackAtConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request reached the server")
	}))
	defer srv.Close()
	resp, err := client(guard).Get(srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrEndpoint) {
		t.Fatalf("got %v", err)
	}
}

func TestTopic(t *testing.T) {
	a, b := Topic("check", "run-1"), Topic("check", "run-2")
	if len(a) != 32 || a == b || a != Topic("check", "run-1") {
		t.Fatalf("%q %q", a, b)
	}
}

func TestNormalize(t *testing.T) {
	p, _ := NewPolicy(DefaultHosts, []string{"http://fake-push:8080"})
	for in, want := range map[string]string{
		"HTTPS://FCM.Googleapis.com.:443/fcm/send/a?b=c": "https://fcm.googleapis.com/fcm/send/a?b=c",
		"http://FAKE-PUSH:8080/push/t":                   "http://fake-push:8080/push/t",
	} {
		got, err := p.Normalize(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	if got, err := p.Normalize("https://evil.example/x"); !errors.Is(err, ErrEndpoint) || got != "" {
		t.Errorf("refused endpoint: %q, %v", got, err)
	}
}
