package thingscloud

import (
	"fmt"
	"testing"
)

func TestClient_Verify(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		server := fakeServer(fakeResponse{200, "verify-success.json"})
		defer server.Close()

		c := New(fmt.Sprintf("http://%s", server.Listener.Addr().String()), "martin@example.com", "")
		v, err := c.Verify()
		if err != nil {
			t.Fatalf("Expected Verification to succeed, but didn't: %q", err.Error())
		}
		if v.Status != AccountStatusActive {
			t.Errorf("Expected account to be %q, but got %q", AccountStatusActive, v.Status)
		}
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		t.Parallel()
		server := fakeBodyServer(200, "{")
		defer server.Close()

		c := New(fmt.Sprintf("http://%s", server.Listener.Addr().String()), "martin@example.com", "")
		if _, err := c.Verify(); err == nil {
			t.Fatal("Expected malformed verify JSON to fail")
		}
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()
		server := fakeServer(fakeResponse{401, "error.json"})
		defer server.Close()

		c := New(fmt.Sprintf("http://%s", server.Listener.Addr().String()), "unknown@example.com", "")
		_, err := c.Verify()
		if err == nil {
			t.Error("Expected Verification to fail, but didn't")
		}
	})

	t.Run("InvalidRequest", func(t *testing.T) {
		t.Parallel()
		c := New("http://example.com", "bad\nemail", "")
		if _, err := c.Verify(); err == nil {
			t.Fatal("Expected invalid account email to return an error")
		}
	})
}
