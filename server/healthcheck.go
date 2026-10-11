package main

import (
	"fmt"
	"net/http"
	"time"
)

const healthcheckTimeout = 3 * time.Second

// healthcheckURL is the public "/" route on loopback, which answers in both
// auth modes and carries no task data (KI-16). The server listens only after
// the initial sync, so a 200 means it is up and serving.
func healthcheckURL(getenv func(string) string) string {
	port := getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	return "http://127.0.0.1:" + port + "/"
}

// runHealthcheck is `things-server healthcheck`, the image's HEALTHCHECK: the
// distroless runtime has no shell or wget to probe the server with.
func runHealthcheck(getenv func(string) string, client *http.Client) error {
	resp, err := client.Get(healthcheckURL(getenv))
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check: %s", resp.Status)
	}
	return nil
}
