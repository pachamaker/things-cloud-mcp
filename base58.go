package thingscloud

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

// base58Alphabet is the Bitcoin alphabet Things uses for identifiers (no 0, O,
// I, l). Things decodes identifiers with BSIdentifierFromBase58String; an
// identifier it cannot decode to 16 bytes crashes every client on sync and
// stays in the history forever (docs/protocol/things-cloud.md, KI-26).
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Identifiers Things emits are 21 or 22 characters long
// (docs/protocol/things-cloud.md, "Identifiers").
const (
	minIdentifierLength = 21
	maxIdentifierLength = 22
)

// maxIdentifierDraws bounds redraws in newUUIDFromReader. About 3% of random
// draws encode to fewer than 22 characters, so 64 failures in a row only
// happen with a broken random source.
const maxIdentifierDraws = 64

// EncodeUUID encodes u as canonical Base58: one leading '1' per leading zero
// byte, so the result always decodes back to 16 bytes.
func EncodeUUID(u uuid.UUID) string {
	zeros := 0
	for zeros < len(u) && u[zeros] == 0 {
		zeros++
	}

	n := new(big.Int).SetBytes(u[:])
	base := big.NewInt(58)
	mod := new(big.Int)
	var encoded []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		encoded = append(encoded, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		encoded = append(encoded, '1')
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

// DecodeUUID decodes a canonical Base58 identifier. It rejects any string
// that does not decode to exactly 16 bytes, which is how a big-integer
// encoder that drops a leading zero byte corrupts a history.
func DecodeUUID(s string) (uuid.UUID, error) {
	var u uuid.UUID
	if s == "" {
		return u, fmt.Errorf("thingscloud: empty identifier")
	}

	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}

	n := new(big.Int)
	base := big.NewInt(58)
	for i := zeros; i < len(s); i++ {
		idx := strings.IndexByte(base58Alphabet, s[i])
		if idx < 0 {
			return u, fmt.Errorf("thingscloud: invalid Base58 character %q in identifier %q", s[i], s)
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(idx)))
	}

	value := n.Bytes()
	if zeros+len(value) != len(u) {
		return u, fmt.Errorf("thingscloud: identifier %q decodes to %d bytes, want 16", s, zeros+len(value))
	}
	copy(u[len(u)-len(value):], value)
	return u, nil
}

// ValidateUUID reports whether s is safe to send to Things Cloud as an
// identifier: 21 or 22 characters that decode to exactly 16 bytes.
func ValidateUUID(s string) error {
	if len(s) < minIdentifierLength || len(s) > maxIdentifierLength {
		return fmt.Errorf("thingscloud: identifier %q has %d characters, want %d-%d", s, len(s), minIdentifierLength, maxIdentifierLength)
	}
	_, err := DecodeUUID(s)
	return err
}

// NewUUID returns a new random identifier for Things Cloud. It panics if the
// system random source fails, like uuid.New.
func NewUUID() string {
	id, err := newUUIDFromReader(rand.Reader)
	if err != nil {
		panic(err)
	}
	return id
}

// newUUIDFromReader redraws until the natural Base58 encoding is exactly 22
// characters. Such identifiers carry all 16 bytes with a non-zero first byte
// and never start with '1': whether Things accepts '1'-padding is disputed
// (KI-26), so generated identifiers stay out of that case entirely.
func newUUIDFromReader(r io.Reader) (string, error) {
	for i := 0; i < maxIdentifierDraws; i++ {
		u, err := uuid.NewRandomFromReader(r)
		if err != nil {
			return "", fmt.Errorf("thingscloud: generate identifier: %w", err)
		}
		if id := EncodeUUID(u); len(id) == maxIdentifierLength && id[0] != '1' {
			return id, nil
		}
	}
	return "", fmt.Errorf("thingscloud: generate identifier: no 22-character draw in %d attempts", maxIdentifierDraws)
}
