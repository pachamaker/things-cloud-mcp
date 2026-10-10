package main

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
)

func TestIsConflictError(t *testing.T) {
	t.Run("typed 409", func(t *testing.T) {
		err := &thingscloud.HTTPError{StatusCode: http.StatusConflict, Status: "409 Conflict"}
		if !isConflictError(err) {
			t.Fatal("expected typed 409 error to be treated as a conflict")
		}
	})

	t.Run("wrapped typed 409", func(t *testing.T) {
		err := fmt.Errorf("wrapped: %w", &thingscloud.HTTPError{StatusCode: http.StatusConflict, Status: "409 Conflict"})
		if !isConflictError(err) {
			t.Fatal("expected wrapped typed 409 error to be treated as a conflict")
		}
	})

	t.Run("non-409 status", func(t *testing.T) {
		err := &thingscloud.HTTPError{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error"}
		if isConflictError(err) {
			t.Fatal("did not expect non-409 status to be treated as a conflict")
		}
	})

	t.Run("untyped History.Write 409", func(t *testing.T) {
		err := fmt.Errorf("wrapped: %w", errors.New("Write failed: 409"))
		if !isConflictError(err) {
			t.Fatal("expected the SDK's untyped commit 409 to be treated as a conflict")
		}
	})

	t.Run("untyped History.Write non-409", func(t *testing.T) {
		if isConflictError(errors.New("Write failed: 4090")) || isConflictError(errors.New("Write failed: 500")) {
			t.Fatal("did not expect other commit statuses to be treated as a conflict")
		}
	})

	t.Run("other text mentioning 409", func(t *testing.T) {
		if isConflictError(errors.New("task 409 not found")) {
			t.Fatal("did not expect unrelated text mentioning 409 to trigger conflict handling")
		}
	})

	t.Run("nil", func(t *testing.T) {
		if isConflictError(nil) {
			t.Fatal("nil is not a conflict")
		}
	})
}
