package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestRunHealthcheck_KI16(t *testing.T) {
	t.Parallel()

	serve := func(t *testing.T, h http.HandlerFunc) string {
		t.Helper()
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		return u.Port()
	}
	env := func(port string) func(string) string {
		return func(key string) string {
			if key == "PORT" {
				return port
			}
			return ""
		}
	}
	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("public root answers 200", func(t *testing.T) {
		t.Parallel()
		var gotPath, gotAuth string
		port := serve(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		})
		if err := runHealthcheck(env(port), client); err != nil {
			t.Fatalf("runHealthcheck = %v, want nil", err)
		}
		if gotPath != "/" {
			t.Errorf("probed %q, want the public health route /", gotPath)
		}
		if gotAuth != "" {
			t.Errorf("probe sent Authorization %q, want none", gotAuth)
		}
	})

	t.Run("non-200 fails", func(t *testing.T) {
		t.Parallel()
		port := serve(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		if err := runHealthcheck(env(port), client); err == nil {
			t.Fatal("runHealthcheck = nil for a 503, want an error")
		}
	})

	t.Run("nothing listening fails", func(t *testing.T) {
		t.Parallel()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, port, _ := net.SplitHostPort(l.Addr().String())
		l.Close()
		if err := runHealthcheck(env(port), client); err == nil {
			t.Fatal("runHealthcheck = nil without a server, want an error")
		}
	})

	t.Run("PORT unset probes the default port", func(t *testing.T) {
		t.Parallel()
		if got := healthcheckURL(env("")); got != "http://127.0.0.1:"+defaultPort+"/" {
			t.Errorf("healthcheckURL = %q, want the default port %s", got, defaultPort)
		}
	})
}
