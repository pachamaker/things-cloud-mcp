package main

import (
	"testing"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
)

func TestGenerateUUID_KI26_AlwaysDecodesTo16Bytes(t *testing.T) {
	t.Parallel()

	for i := 0; i < 5000; i++ {
		id := generateUUID()
		if len(id) != 22 {
			t.Fatalf("generateUUID() = %q has %d characters, want 22", id, len(id))
		}
		if err := thingscloud.ValidateUUID(id); err != nil {
			t.Fatalf("generateUUID() = %q is not a safe identifier: %v", id, err)
		}
	}
}

func TestIsBase58UUID_KI26(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"native 22 chars", "VJ1edXTP9q3PmFDUuy8EQh", true},
		{"native with leading 1", "14q4keicwiREVK8EKAuowZ", true},
		{"KI-26 poison: leading zero byte dropped", "4ZBpHuxHoLyQUMwetsL8D", false},
		{"value overflows 16 bytes", "zzzzzzzzzzzzzzzzzzzzzz", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isBase58UUID(tt.id); got != tt.want {
				t.Errorf("isBase58UUID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
