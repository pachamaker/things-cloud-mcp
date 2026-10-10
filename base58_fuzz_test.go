package thingscloud

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

// FuzzUUIDRoundTrip: every 16-byte value survives EncodeUUID -> DecodeUUID.
// The pre-KI-26 encoder broke this for any value with a leading zero byte.
func FuzzUUIDRoundTrip(f *testing.F) {
	f.Add(make([]byte, 16))
	f.Add(leadingZeroUUID[:])
	f.Add([]byte{0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13})
	f.Add(bytes.Repeat([]byte{0xff}, 16))

	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) != 16 {
			return
		}
		var u uuid.UUID
		copy(u[:], b)

		s := EncodeUUID(u)
		if len(s) == 0 || len(s) > maxIdentifierLength {
			t.Fatalf("EncodeUUID(% x) = %q has %d characters", b, s, len(s))
		}
		back, err := DecodeUUID(s)
		if err != nil {
			t.Fatalf("DecodeUUID(%q) from % x: %v", s, b, err)
		}
		if back != u {
			t.Fatalf("round trip: % x -> %q -> % x", b, s, back[:])
		}
	})
}

// FuzzValidateUUIDIsSound: whatever ValidateUUID accepts is 21-22 characters,
// decodes to exactly 16 bytes and is the canonical encoding of those bytes.
func FuzzValidateUUIDIsSound(f *testing.F) {
	f.Add("VJ1edXTP9q3PmFDUuy8EQh")
	f.Add("14q4keicwiREVK8EKAuowZ")
	f.Add("4ZBpHuxHoLyQUMwetsL8D")
	f.Add("zzzzzzzzzzzzzzzzzzzzzz")
	f.Add("6f9b2c1e-8a4d-4e5f-9c3b-2a1d0e9f8b7c")
	f.Add("")
	f.Add("\x00\xff")

	f.Fuzz(func(t *testing.T, s string) {
		_, _ = DecodeUUID(s)
		if err := ValidateUUID(s); err != nil {
			return
		}
		if len(s) < minIdentifierLength || len(s) > maxIdentifierLength {
			t.Fatalf("ValidateUUID accepted %q with %d characters", s, len(s))
		}
		u, err := DecodeUUID(s)
		if err != nil {
			t.Fatalf("ValidateUUID accepted %q but DecodeUUID rejects it: %v", s, err)
		}
		if got := EncodeUUID(u); got != s {
			t.Fatalf("ValidateUUID accepted non-canonical %q (canonical %q)", s, got)
		}
	})
}

// FuzzNewUUIDFromReader: for any random source, a generated identifier is
// exactly 22 characters, never starts with '1' and decodes to 16 bytes with a
// non-zero first byte, so it is safe whether or not Things pads with '1'.
func FuzzNewUUIDFromReader(f *testing.F) {
	f.Add(bytes.Repeat([]byte{0xc3}, 16))
	f.Add(append(make([]byte, 16), bytes.Repeat([]byte{0xc3}, 16)...))
	f.Add(append([]byte{0x01}, bytes.Repeat([]byte{0xab}, 31)...))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, random []byte) {
		s, err := newUUIDFromReader(bytes.NewReader(random))
		if err != nil {
			return
		}
		if len(s) != maxIdentifierLength || s[0] == '1' {
			t.Fatalf("newUUIDFromReader(% x) = %q, want 22 characters without leading '1'", random, s)
		}
		u, err := DecodeUUID(s)
		if err != nil {
			t.Fatalf("newUUIDFromReader(% x) = %q does not decode: %v", random, s, err)
		}
		if u[0] == 0 {
			t.Fatalf("newUUIDFromReader(% x) = %q has a leading zero byte", random, s)
		}
		if err := ValidateUUID(s); err != nil {
			t.Fatalf("newUUIDFromReader(% x) = %q fails ValidateUUID: %v", random, s, err)
		}
	})
}
