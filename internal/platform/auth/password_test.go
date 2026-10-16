package auth

import (
	"errors"
	"strings"
	"testing"
)

func newTestHasher(t *testing.T) *Hasher {
	t.Helper()
	h, err := NewHasher(PasswordParams{MemoryKiB: 64, Time: 1, Threads: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return h
}

func TestHashAndVerify(t *testing.T) {
	h := newTestHasher(t)
	const password = "correct horse battery"

	encoded, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("hash is not PHC encoded with embedded params: %s", encoded)
	}
	if strings.Contains(encoded, password) {
		t.Error("hash contains the plaintext password")
	}

	ok, err := h.Verify(encoded, password)
	if err != nil || !ok {
		t.Errorf("Verify(correct) = %v, %v; want true, nil", ok, err)
	}
	ok, err = h.Verify(encoded, "wrong horse battery")
	if err != nil || ok {
		t.Errorf("Verify(wrong) = %v, %v; want false, nil", ok, err)
	}

	again, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash again: %v", err)
	}
	if again == encoded {
		t.Error("two hashes of the same password are identical: salt is not random")
	}
}

func TestHashEnforcesLengthRules(t *testing.T) {
	h := newTestHasher(t)
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"11 characters", strings.Repeat("a", 11), ErrPasswordTooShort},
		{"12 characters", strings.Repeat("a", 12), nil},
		{"12 multibyte characters", strings.Repeat("é", 12), nil},
		{"11 multibyte characters", strings.Repeat("é", 11), ErrPasswordTooShort},
		{"over the byte cap", strings.Repeat("a", MaxPasswordBytes+1), ErrPasswordTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.Hash(tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyUsesEmbeddedParams(t *testing.T) {
	weak := newTestHasher(t)
	strong, err := NewHasher(PasswordParams{MemoryKiB: 128, Time: 2, Threads: 2})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	encoded, err := weak.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	ok, err := strong.Verify(encoded, "correct horse battery")
	if err != nil || !ok {
		t.Errorf("a hasher with different params must still verify old hashes: %v, %v", ok, err)
	}
	if !strong.NeedsRehash(encoded) {
		t.Error("NeedsRehash = false for a hash made with different params")
	}
	if weak.NeedsRehash(encoded) {
		t.Error("NeedsRehash = true for a hash made with the current params")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h := newTestHasher(t)
	salt := strings.Repeat("A", 22)
	hash := strings.Repeat("A", 43)

	tests := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"plaintext", "not-a-hash"},
		{"wrong algorithm", "$argon2i$v=19$m=64,t=1,p=1$" + salt + "$" + hash},
		{"wrong version", "$argon2id$v=18$m=64,t=1,p=1$" + salt + "$" + hash},
		{"zero memory", "$argon2id$v=19$m=0,t=1,p=1$" + salt + "$" + hash},
		{"too many threads", "$argon2id$v=19$m=64,t=1,p=256$" + salt + "$" + hash},
		{"bad params", "$argon2id$v=19$memory=64$" + salt + "$" + hash},
		{"bad salt encoding", "$argon2id$v=19$m=64,t=1,p=1$!!!$" + hash},
		{"bad hash encoding", "$argon2id$v=19$m=64,t=1,p=1$" + salt + "$!!!"},
		{"missing hash", "$argon2id$v=19$m=64,t=1,p=1$" + salt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := h.Verify(tt.encoded, "correct horse battery")
			if ok || !errors.Is(err, ErrInvalidHash) {
				t.Errorf("Verify = %v, %v; want false, ErrInvalidHash", ok, err)
			}
			if !h.NeedsRehash(tt.encoded) {
				t.Error("NeedsRehash = false for a malformed hash")
			}
		})
	}
}

func TestDummyHashIsAValidHashAndVerifyDummyNeverMatches(t *testing.T) {
	h := newTestHasher(t)

	if _, _, _, err := decodeHash(h.dummy); err != nil {
		t.Fatalf("dummy hash does not decode: %v", err)
	}
	if ok, err := h.Verify(h.dummy, "anything at all here"); err != nil || ok {
		t.Errorf("dummy hash verified a guess: %v, %v", ok, err)
	}
	h.VerifyDummy("anything at all here")
}

func TestVerifyRejectsOversizedPasswordsWithoutHashing(t *testing.T) {
	h := newTestHasher(t)
	encoded, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := h.Verify(encoded, strings.Repeat("a", MaxPasswordBytes+1))
	if ok || err != nil {
		t.Errorf("Verify(oversized) = %v, %v; want false, nil", ok, err)
	}
}

func TestNewHasherRejectsBadParams(t *testing.T) {
	tests := []struct {
		name   string
		params PasswordParams
	}{
		{"zero time", PasswordParams{MemoryKiB: 64, Time: 0, Threads: 1}},
		{"zero threads", PasswordParams{MemoryKiB: 64, Time: 1, Threads: 0}},
		{"memory below 8 per thread", PasswordParams{MemoryKiB: 15, Time: 1, Threads: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewHasher(tt.params); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
