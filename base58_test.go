package thingscloud

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

// Expected strings below were computed with an independent Base58
// implementation, not with EncodeUUID.

var leadingZeroUUID = uuid.UUID{0x00, 0x7f, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}

func TestEncodeUUID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		u    uuid.UUID
		want string
	}{
		{"max value", uuid.UUID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, "YcVfxkQb6JRzqk5kF2tNLv"},
		{"leading zero byte is padded with 1", leadingZeroUUID, "14ZBpHuxHoLyQUMwetsL8D"},
		{"all zero", uuid.UUID{}, "1111111111111111"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := EncodeUUID(tt.u); got != tt.want {
				t.Errorf("EncodeUUID(% x) = %q, want %q", tt.u[:], got, tt.want)
			}
		})
	}
}

func TestDecodeUUID_RealThingsIdentifiers(t *testing.T) {
	t.Parallel()

	// VJ1e…, FQxa…, BVU8…, Q9si… were captured from Things.app sync traffic
	// (upstream arthursoares/things-cloud-sdk docs/client-side-bugs.md).
	// fxsS… and 14q4… are identifiers Things derives for legacy objects
	// (upstream base58_test.go): one is 21 characters, one starts with '1'.
	ids := []string{
		"VJ1edXTP9q3PmFDUuy8EQh",
		"FQxaqvLBkbR5q2Q5oRoknc",
		"BVU8qZ9dNjrdxLvDHPvfDS",
		"Q9sihFX2SsvGaz6vv4J2Hf",
		"fxsSvCT97pJn3XZ4wp5t4",
		"14q4keicwiREVK8EKAuowZ",
	}
	for _, s := range ids {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			u, err := DecodeUUID(s)
			if err != nil {
				t.Fatalf("DecodeUUID(%q) error: %v", s, err)
			}
			if got := EncodeUUID(u); got != s {
				t.Errorf("EncodeUUID(DecodeUUID(%q)) = %q, want round trip", s, got)
			}
		})
	}
}

func TestValidateUUID_KI26(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"native 22 chars", "VJ1edXTP9q3PmFDUuy8EQh", true},
		{"native 21 chars", "fxsSvCT97pJn3XZ4wp5t4", true},
		{"native with leading 1", "14q4keicwiREVK8EKAuowZ", true},
		{"canonical leading zero byte", "14ZBpHuxHoLyQUMwetsL8D", true},
		{"KI-26 poison: leading zero byte dropped, 21 chars", "4ZBpHuxHoLyQUMwetsL8D", false},
		{"KI-26 poison: leading zero byte dropped, 20 chars", "2r2rVJWynBQLDNnYaPW2", false},
		{"extra leading 1 decodes to 17 bytes", "1CxxvKc7uwRL2SgurcPWNN", false},
		{"value overflows 16 bytes", "zzzzzzzzzzzzzzzzzzzzzz", false},
		{"all zero canonical is not 21-22 chars", "1111111111111111", false},
		{"23 chars", "1YcVfxkQb6JRzqk5kF2tNLv", false},
		{"empty", "", false},
		{"hyphenated UUID", "6f9b2c1e-8a4d-4e5f-9c3b-2a1d0e9f8b7c", false},
		{"contains 0", "VJ0edXTP9q3PmFDUuy8EQh", false},
		{"contains O", "VJOedXTP9q3PmFDUuy8EQh", false},
		{"contains I", "VJIedXTP9q3PmFDUuy8EQh", false},
		{"contains l", "VJledXTP9q3PmFDUuy8EQh", false},
		{"surrounding space", " VJ1edXTP9q3PmFDUuy8EQh", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateUUID(tt.id)
			if tt.valid && err != nil {
				t.Errorf("ValidateUUID(%q) = %v, want nil", tt.id, err)
			}
			if !tt.valid && err == nil {
				t.Errorf("ValidateUUID(%q) = nil, want error", tt.id)
			}
		})
	}
}

func repeatByte(b byte, n int) []byte {
	return bytes.Repeat([]byte{b}, n)
}

func TestNewUUIDFromReader_KI26(t *testing.T) {
	t.Parallel()

	// 0xc3 x16 with v4 version/variant bits applied encodes to 22 characters.
	const want = "RB5sGLYgbPhnWcxTcpBCSn"
	goodDraw := repeatByte(0xc3, 16)
	leadingZeroDraw := append([]byte{0x00}, repeatByte(0x5a, 15)...)
	shortDraw := append([]byte{0x01}, repeatByte(0xab, 15)...)

	tests := []struct {
		name   string
		random []byte
	}{
		{"first draw is kept", goodDraw},
		{"leading zero byte is redrawn", append(append([]byte{}, leadingZeroDraw...), goodDraw...)},
		{"21-character encoding is redrawn", append(append([]byte{}, shortDraw...), goodDraw...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := bytes.NewReader(tt.random)
			got, err := newUUIDFromReader(r)
			if err != nil {
				t.Fatalf("newUUIDFromReader() error: %v", err)
			}
			if got != want {
				t.Errorf("newUUIDFromReader() = %q, want %q", got, want)
			}
			if r.Len() != 0 {
				t.Errorf("newUUIDFromReader() left %d random bytes unread, want every draw consumed", r.Len())
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestNewUUIDFromReader_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    func() *bytes.Reader
	}{
		{"exhausted source", func() *bytes.Reader { return bytes.NewReader(repeatByte(0xc3, 8)) }},
		{"only unsafe draws before exhaustion", func() *bytes.Reader { return bytes.NewReader(repeatByte(0x00, 64)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := newUUIDFromReader(tt.r()); err == nil {
				t.Errorf("newUUIDFromReader() = %q, want error", got)
			}
		})
	}

	t.Run("source that never yields a safe draw", func(t *testing.T) {
		t.Parallel()
		if got, err := newUUIDFromReader(zeroReader{}); err == nil {
			t.Errorf("newUUIDFromReader() = %q, want error", got)
		}
	})
}
