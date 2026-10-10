//go:build e2e && fakecloud

package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type scriptedCloud struct {
	statuses []int
	calls    int
}

func (s *scriptedCloud) RoundTrip(req *http.Request) (*http.Response, error) {
	status := http.StatusOK
	if s.calls < len(s.statuses) {
		status = s.statuses[s.calls]
	}
	s.calls++
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    req,
	}, nil
}

func TestSandboxTransport_KI27_RetriesGetAfter429(t *testing.T) {
	backoff := []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	get := func(t *testing.T, statuses []int) (*http.Response, *scriptedCloud, *cloudTransport) {
		t.Helper()
		cloud := &scriptedCloud{statuses: statuses}
		c := &cloudTransport{base: cloud, rateLimitBackoff: backoff}
		req, _ := http.NewRequest(http.MethodGet, "https://"+thingsCloudHost+"/version/1/history/x/items", nil)
		resp, err := c.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		return resp, cloud, c
	}

	t.Run("recovers", func(t *testing.T) {
		resp, cloud, c := get(t, []int{429, 429})
		if resp.StatusCode != http.StatusOK || cloud.calls != 3 {
			t.Errorf("status %d after %d calls, want 200 after 3", resp.StatusCode, cloud.calls)
		}
		if st := c.stats(); st.RateLimited || st.Retried429 != 1 {
			t.Errorf("stats %+v, want RateLimited false (the run goes on) and Retried429 1", st)
		}
	})
	t.Run("gives up", func(t *testing.T) {
		resp, cloud, c := get(t, []int{429, 429, 429, 429, 429})
		if resp.StatusCode != http.StatusTooManyRequests || cloud.calls != len(backoff)+1 {
			t.Errorf("status %d after %d calls, want 429 after %d", resp.StatusCode, cloud.calls, len(backoff)+1)
		}
		if !c.stats().RateLimited {
			t.Error("an exhausted 429 must stop the run (RateLimited)")
		}
	})
	t.Run("commit is not retried", func(t *testing.T) {
		cloud := &scriptedCloud{statuses: []int{429}}
		c := &cloudTransport{base: cloud, rateLimitBackoff: backoff, firewall: newWriteFirewall(time.Now)}
		req, _ := http.NewRequest(http.MethodPost, "https://"+thingsCloudHost+"/version/1/history/x/items", strings.NewReader(`{}`))
		resp, err := c.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		if resp.StatusCode != http.StatusTooManyRequests || cloud.calls != 1 {
			t.Errorf("POST: status %d after %d calls, want 429 after 1", resp.StatusCode, cloud.calls)
		}
	})
}

func TestSandboxTransport_KI27_RetryAfter(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 30 * time.Second},
		{"7", 7 * time.Second},
		{"0", 0},
		{"86400", sandboxRetryAfterCap},
		{"Wed, 21 Oct 2026 07:28:00 GMT", 30 * time.Second},
		{"-3", 30 * time.Second},
	}
	for _, tt := range tests {
		if got := retryAfter(tt.header, 30*time.Second); got != tt.want {
			t.Errorf("retryAfter(%q) = %s, want %s", tt.header, got, tt.want)
		}
	}
}
