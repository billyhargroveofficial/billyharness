package webtools

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientRejectsPublicThenPrivateRebinding(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("10.0.0.5"),
	}}
	dialer := &scriptedDialer{responses: []string{okResponse("should-not-run")}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	_, err := client.Get(context.Background(), "http://example.test/", 1024)
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("expected non-public rebinding error, got %v", err)
	}
	if got := dialer.CallCount(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
}

func TestClientRejectsRedirectToPrivateIPBeforeSecondDial(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("93.184.216.34"),
	}}
	dialer := &scriptedDialer{responses: []string{
		redirectResponse("http://192.168.0.10/private"),
		okResponse("should-not-run"),
	}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	_, err := client.Get(context.Background(), "http://example.test/", 1024)
	if err == nil || !strings.Contains(err.Error(), "non-public IP") {
		t.Fatalf("expected redirect non-public IP error, got %v", err)
	}
	if got := dialer.CallCount(); got != 1 {
		t.Fatalf("dial calls = %d, want only initial public request", got)
	}
}

func TestClientRejectsLocalhostAndRFC1918Targets(t *testing.T) {
	client := Client{DialContext: (&scriptedDialer{}).DialContext, Timeout: time.Second}
	for _, rawURL := range []string{
		"http://localhost:8080/",
		"http://service.localhost/",
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
	} {
		t.Run(rawURL, func(t *testing.T) {
			_, err := client.Get(context.Background(), rawURL, 1024)
			if err == nil {
				t.Fatal("expected private/local target rejection")
			}
		})
	}
}

func TestClientFetchesNormalPublicHost(t *testing.T) {
	resolver := &scriptedResolver{answers: [][]net.IPAddr{
		ipAddrs("93.184.216.34"),
		ipAddrs("93.184.216.34"),
	}}
	dialer := &scriptedDialer{responses: []string{okResponse("ok")}}
	client := Client{Resolver: resolver, DialContext: dialer.DialContext, Timeout: time.Second}

	resp, err := client.Get(context.Background(), "http://example.test/page", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "ok" || resp.ContentType != "text/plain" || resp.URL != "http://example.test/page" || resp.StatusCode != 200 {
		t.Fatalf("response = %#v body=%q", resp, resp.Body)
	}
	calls := dialer.Calls()
	if len(calls) != 1 || calls[0] != "93.184.216.34:80" {
		t.Fatalf("dial calls = %#v", calls)
	}
}

type scriptedResolver struct {
	mu      sync.Mutex
	answers [][]net.IPAddr
}

func (r *scriptedResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.answers) == 0 {
		return nil, fmt.Errorf("no resolver answer for %s", host)
	}
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return answer, nil
}

type scriptedDialer struct {
	mu        sync.Mutex
	calls     []string
	responses []string
}

func (d *scriptedDialer) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, address)
	if len(d.responses) == 0 {
		d.mu.Unlock()
		return nil, fmt.Errorf("no response for %s", address)
	}
	response := d.responses[0]
	d.responses = d.responses[1:]
	d.mu.Unlock()

	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		var request string
		buf := make([]byte, 256)
		for !strings.Contains(request, "\r\n\r\n") && len(request) < 8192 {
			n, err := server.Read(buf)
			if n > 0 {
				request += string(buf[:n])
			}
			if err != nil {
				return
			}
		}
		_, _ = io.WriteString(server, response)
	}()
	return client, nil
}

func (d *scriptedDialer) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *scriptedDialer) CallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func ipAddrs(values ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		out = append(out, net.IPAddr{IP: net.ParseIP(value)})
	}
	return out
}

func okResponse(body string) string {
	return fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
}

func redirectResponse(location string) string {
	return "HTTP/1.1 302 Found\r\nLocation: " + location + "\r\nContent-Length: 0\r\n\r\n"
}
