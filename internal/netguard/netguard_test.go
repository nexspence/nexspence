package netguard

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",
		"::1",
		"169.254.169.254",
		"10.0.0.5",
		"192.168.1.1",
		"172.16.0.1",
	}
	for _, ip := range blocked {
		if !isBlockedIP(ip) {
			t.Errorf("expected %q to be blocked", ip)
		}
	}

	allowed := []string{
		"8.8.8.8",
		"1.1.1.1",
	}
	for _, ip := range allowed {
		if isBlockedIP(ip) {
			t.Errorf("expected %q to be allowed", ip)
		}
	}

	// Unparseable input must fail closed (blocked).
	if !isBlockedIP("not-an-ip") {
		t.Errorf("expected unparseable host to be blocked")
	}
}

func TestControl(t *testing.T) {
	if err := control("tcp", "10.0.0.5:80", nil); err == nil {
		t.Error("expected blocked internal address to error")
	}
	if err := control("tcp", "8.8.8.8:80", nil); err != nil {
		t.Errorf("expected allowed address to pass, got %v", err)
	}
	// Address without a port is invalid → error.
	if err := control("tcp", "8.8.8.8", nil); err == nil {
		t.Error("expected invalid dial address to error")
	}
}

func TestDialControl_DelegatesToControl(t *testing.T) {
	if err := DialControl("tcp", "127.0.0.1:443", nil); err == nil {
		t.Error("expected DialControl to block loopback")
	}
	if err := DialControl("tcp", "1.1.1.1:443", nil); err != nil {
		t.Errorf("expected DialControl to allow public address, got %v", err)
	}
}

func TestClient_Construction(t *testing.T) {
	c := Client(7 * time.Second)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Timeout != 7*time.Second {
		t.Errorf("expected timeout 7s, got %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Fatal("expected an *http.Transport with a DialContext")
	}
}

func TestClient_BlocksInternalRequest(t *testing.T) {
	c := Client(2 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:9/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected request to an internal address to be blocked")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected SSRF block error, got %v", err)
	}
}
