// Package auth provides password hashing, access and refresh tokens, and the
// HTTP middleware that authenticates and authorizes requests.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	// MinPasswordLength is the minimum password length in characters.
	MinPasswordLength = 12
	// MaxPasswordBytes bounds the hashing work a single request can force.
	MaxPasswordBytes = 1024

	saltLength = 16
	hashLength = 32
)

var (
	// ErrPasswordTooShort is returned when a password has fewer than MinPasswordLength characters.
	ErrPasswordTooShort = errors.New("auth: password is too short")
	// ErrPasswordTooLong is returned when a password exceeds MaxPasswordBytes.
	ErrPasswordTooLong = errors.New("auth: password is too long")
	// ErrInvalidHash is returned when a stored hash is not a valid argon2id PHC string.
	ErrInvalidHash = errors.New("auth: invalid password hash")
)

// PasswordParams are the argon2id cost parameters.
type PasswordParams struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

func (p PasswordParams) validate() error {
	if p.Time == 0 || p.Threads == 0 || p.MemoryKiB < 8*uint32(p.Threads) {
		return errors.New("auth: invalid argon2 parameters")
	}
	return nil
}

// Hasher hashes and verifies passwords with argon2id and PHC-encoded output,
// so parameters and salt travel with each hash and can change later.
type Hasher struct {
	params PasswordParams
	dummy  string
}

// NewHasher validates params and precomputes the dummy hash that Login uses
// when the account does not exist, so response time does not reveal which
// emails are registered.
func NewHasher(params PasswordParams) (*Hasher, error) {
	if err := params.validate(); err != nil {
		return nil, err
	}
	h := &Hasher{params: params}

	filler := make([]byte, 16)
	if _, err := rand.Read(filler); err != nil {
		return nil, fmt.Errorf("auth: dummy password: %w", err)
	}
	dummy, err := h.encode(base64.RawStdEncoding.EncodeToString(filler))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

// Hash enforces the length rules and returns a PHC-encoded argon2id hash.
func (h *Hasher) Hash(password string) (string, error) {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	if len(password) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	return h.encode(password)
}

func (h *Hasher) encode(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.MemoryKiB, h.params.Threads, hashLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.MemoryKiB, h.params.Time, h.params.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify checks password against an encoded hash using the parameters embedded
// in that hash and a constant-time comparison. It returns an error only when
// the hash itself is malformed.
func (h *Hasher) Verify(encoded, password string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	if len(password) > MaxPasswordBytes {
		return false, nil
	}
	got := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyDummy burns the same work as a real verification against the dummy
// hash. Call it when the account does not exist.
func (h *Hasher) VerifyDummy(password string) {
	_, _ = h.Verify(h.dummy, password)
}

// NeedsRehash reports whether encoded was made with different parameters than
// this Hasher uses, or cannot be parsed. Callers rehash after a successful login.
func (h *Hasher) NeedsRehash(encoded string) bool {
	params, _, hash, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return params != h.params || len(hash) != hashLength
}

func decodeHash(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return PasswordParams{}, nil, nil, ErrInvalidHash
	}

	var memory, passes, threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil {
		return PasswordParams{}, nil, nil, ErrInvalidHash
	}
	if memory == 0 || passes == 0 || threads == 0 || threads > 255 {
		return PasswordParams{}, nil, nil, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return PasswordParams{}, nil, nil, ErrInvalidHash
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(hash) == 0 {
		return PasswordParams{}, nil, nil, ErrInvalidHash
	}

	return PasswordParams{MemoryKiB: memory, Time: passes, Threads: uint8(threads)}, salt, hash, nil
}
