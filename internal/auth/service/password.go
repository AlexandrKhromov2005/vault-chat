// Package service contains the business logic of the auth service.
package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2idParams configures the Argon2id key derivation function.
type Argon2idParams struct {
	MemoryKiB   uint32
	Time        uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2idParams returns the OWASP-recommended Argon2id parameters
// (64 MiB memory, 3 iterations, 2 lanes).
func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		MemoryKiB:   65536,
		Time:        3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// Argon2idHasher hashes and verifies passwords using Argon2id in the PHC
// string format.
type Argon2idHasher struct {
	params Argon2idParams
}

// NewArgon2idHasher creates a hasher with the given parameters. Parameters
// are stored in every produced hash, so verification does not depend on the
// hasher configuration.
func NewArgon2idHasher(params Argon2idParams) *Argon2idHasher {
	return &Argon2idHasher{params: params}
}

// Hash derives an Argon2id hash of password with a fresh random salt and
// returns it in PHC string format.
func (h *Argon2idHasher) Hash(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}

	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt,
		h.params.Time, h.params.MemoryKiB, h.params.Parallelism, h.params.KeyLength)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.MemoryKiB, h.params.Time, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// Verify reports whether password matches the PHC-encoded hash. Malformed
// hashes yield an error rather than a panic or a false match.
func (h *Argon2idHasher) Verify(password, encodedHash string) (bool, error) {
	params, salt, key, err := decodePHC(encodedHash)
	if err != nil {
		return false, err
	}

	candidate := argon2.IDKey([]byte(password), salt,
		params.Time, params.MemoryKiB, params.Parallelism, uint32(len(key)))

	return subtle.ConstantTimeCompare(candidate, key) == 1, nil
}

// decodePHC parses an Argon2id PHC string:
// $argon2id$v=19$m=<mem>,t=<time>,p=<lanes>$<salt>$<key>
func decodePHC(encoded string) (Argon2idParams, []byte, []byte, error) {
	var params Argon2idParams

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params, nil, nil, errors.New("malformed PHC string: expected argon2id format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params, nil, nil, fmt.Errorf("unsupported argon2 version %q", parts[2])
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&params.MemoryKiB, &params.Time, &params.Parallelism); err != nil {
		return params, nil, nil, fmt.Errorf("malformed PHC parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return params, nil, nil, fmt.Errorf("malformed PHC salt: %w", err)
	}

	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return params, nil, nil, fmt.Errorf("malformed PHC key: %w", err)
	}

	if len(salt) == 0 || len(key) == 0 {
		return params, nil, nil, errors.New("malformed PHC string: empty salt or key")
	}

	return params, salt, key, nil
}
