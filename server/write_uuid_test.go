package main

import (
	"testing"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
)

func TestGenerateUUID_KI26_AlwaysDecodesTo16Bytes(t *testing.T) {
	t.Parallel()

	// A leading zero byte occurs in ~1/256 draws and a 21-character natural
	// encoding in ~3%, so 5000 draws hit both with near certainty.
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
		{"native 21 chars", "fxsSvCT97pJn3XZ4wp5t4", true},
		{"native with leading 1", "14q4keicwiREVK8EKAuowZ", true},
		{"KI-26 poison: leading zero byte dropped, 21 chars", "4ZBpHuxHoLyQUMwetsL8D", false},
		{"KI-26 poison: leading zero byte dropped, 20 chars", "2r2rVJWynBQLDNnYaPW2", false},
		{"value overflows 16 bytes", "zzzzzzzzzzzzzzzzzzzzzz", false},
		{"too long", "VJ1edXTP9q3PmFDUuy8EQhVJ1edXTP9q", false},
		{"hyphenated UUID", "6f9b2c1e-8a4d-4e5f-9c3b-2a1d0e9f8b7c", false},
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
