//go:build e2e && heading && fakecloud

package main

// Mocks exercise the suite but never become native evidence or an exported fixture.
func init() { headingCaptureCheck = func() error { return nil } }
