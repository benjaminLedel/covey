package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGuardRefusesTheHost is #692: a URL an organisation configures must not
// let the control plane reach its own loopback services or the metadata
// endpoint — while the company network stays reachable and unmarked requests
// are left alone.
func TestGuardRefusesTheHost(t *testing.T) {
	for _, c := range []struct {
		ip      string
		refused bool
	}{
		{"127.0.0.1", true}, {"::1", true}, {"169.254.169.254", true}, {"fe80::1", true},
		{"0.0.0.0", true}, {"224.0.0.1", true},
		{"10.1.2.3", false}, {"192.168.1.10", false}, {"172.20.0.5", false}, {"93.184.216.34", false},
	} {
		if got := Refused(net.ParseIP(c.ip)); got != c.refused {
			t.Errorf("Refused(%s) = %v, expected %v", c.ip, got, c.refused)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("inside"))
	}))
	defer srv.Close()
	client := &http.Client{Transport: Transport()}

	// A guarded request to loopback is refused at dial time …
	req, _ := http.NewRequestWithContext(Guard(context.Background()), http.MethodGet, srv.URL, nil)
	if _, err := client.Do(req); !errors.Is(err, ErrRefused) {
		t.Fatalf("guarded request to loopback: %v, expected ErrRefused", err)
	}
	if err := CheckURL(context.Background(), srv.URL); !errors.Is(err, ErrRefused) {
		t.Fatalf("CheckURL(%s) = %v", srv.URL, err)
	}
	// … an unmarked one is not the guard's business …
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if res, err := client.Do(req); err != nil {
		t.Fatalf("unmarked request: %v", err)
	} else {
		res.Body.Close()
	}
	// … and an operator's exception lets the guarded one through.
	if err := SetAllow("127.0.0.1, ::1"); err != nil {
		t.Fatal(err)
	}
	defer SetAllow("")
	req, _ = http.NewRequestWithContext(Guard(context.Background()), http.MethodGet, srv.URL, nil)
	if res, err := client.Do(req); err != nil {
		t.Fatalf("allowed loopback: %v", err)
	} else {
		res.Body.Close()
	}
	if err := SetAllow("not-an-address"); err == nil {
		t.Error("an invalid entry must be reported")
	}
}
