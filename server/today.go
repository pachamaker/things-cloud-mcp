package main

import (
	"fmt"
	"time"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

// ownerZone decides the owner's calendar day (ADR-0003, KI-03). main sets it
// from THINGS_TIMEZONE before serving; the sandbox harness leaves it UTC.
var ownerZone = time.UTC

// ownerToday is the owner's current calendar day. A request computes it once
// and passes it down, so its checks and writes never straddle two days.
func ownerToday() civil.Date {
	return civil.Today(civil.SystemClock{}, ownerZone)
}

// ownerTimezone parses THINGS_TIMEZONE. Unset keeps the UTC day of earlier
// versions, so an upgrade does not stop a running deployment; main warns.
func ownerTimezone(raw string) (*time.Location, error) {
	if raw == "" {
		return time.UTC, nil
	}
	loc, err := civil.LoadLocation(raw)
	if err != nil {
		return nil, fmt.Errorf("THINGS_TIMEZONE: %w", err)
	}
	return loc, nil
}
