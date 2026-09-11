package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	DefaultArgon2Memory      uint32 = 64 * 1024
	DefaultArgon2Iterations  uint32 = 3
	DefaultArgon2Parallelism uint8  = 2
	argon2SaltSize                  = 16
	argon2KeySize                   = 32
	maxEncodedHashLength            = 512

	minArgon2Memory      uint32 = 19 * 1024
	maxArgon2Memory      uint32 = 1024 * 1024
	minArgon2Iterations  uint32 = 1
	maxArgon2Iterations  uint32 = 10
	minArgon2Parallelism uint8  = 1
	maxArgon2Parallelism uint8  = 8
)

var ErrMalformedPasswordHash = errors.New("malformed password hash")

type PasswordHasher struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
}

func NewPasswordHasher() PasswordHasher {
	return PasswordHasher{
		Memory: DefaultArgon2Memory, Iterations: DefaultArgon2Iterations,
		Parallelism: DefaultArgon2Parallelism,
	}
}

func (h PasswordHasher) Hash(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	return h.hash(password)
}

// HashBootstrapAdminPassword hashes the one-time installation credential. It is
// deliberately the only password-policy exception: all user-selected passwords
// must go through Hash and satisfy the normal policy.
func (h PasswordHasher) HashBootstrapAdminPassword() (string, error) {
	return h.hash("admin")
}

func (h PasswordHasher) hash(password string) (string, error) {
	params := h.withDefaults()
	if !validArgon2Parameters(params.Memory, params.Iterations, params.Parallelism) {
		return "", errors.New("Argon2id parameters outside allowed range")
	}
	salt := make([]byte, argon2SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, argon2KeySize)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.Memory, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (h PasswordHasher) Verify(password, encoded string) (bool, error) {
	if len(password) > 1024 {
		return false, nil
	}
	if len(encoded) == 0 || len(encoded) > maxEncodedHashLength {
		return false, ErrMalformedPasswordHash
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrMalformedPasswordHash
	}
	version, ok := parsePrefixedUint(parts[2], "v=", 8)
	if !ok || int(version) != argon2.Version {
		return false, ErrMalformedPasswordHash
	}
	memory, iterations, parallelism, ok := parseParameters(parts[3])
	if !ok || !validArgon2Parameters(memory, iterations, parallelism) {
		return false, ErrMalformedPasswordHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < argon2SaltSize || len(salt) > 64 {
		return false, ErrMalformedPasswordHash
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(want) != argon2KeySize {
		return false, ErrMalformedPasswordHash
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func (h PasswordHasher) withDefaults() PasswordHasher {
	if h.Memory == 0 {
		h.Memory = DefaultArgon2Memory
	}
	if h.Iterations == 0 {
		h.Iterations = DefaultArgon2Iterations
	}
	if h.Parallelism == 0 {
		h.Parallelism = DefaultArgon2Parallelism
	}
	return h
}

func parseParameters(encoded string) (uint32, uint32, uint8, bool) {
	parts := strings.Split(encoded, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	memory, memoryOK := parsePrefixedUint(parts[0], "m=", 32)
	iterations, iterationsOK := parsePrefixedUint(parts[1], "t=", 32)
	parallelism, parallelismOK := parsePrefixedUint(parts[2], "p=", 8)
	if !memoryOK || !iterationsOK || !parallelismOK {
		return 0, 0, 0, false
	}
	return uint32(memory), uint32(iterations), uint8(parallelism), true
}

func parsePrefixedUint(value, prefix string, bitSize int) (uint64, bool) {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value[len(prefix):], 10, bitSize)
	return parsed, err == nil
}

func validArgon2Parameters(memory, iterations uint32, parallelism uint8) bool {
	return memory >= minArgon2Memory && memory <= maxArgon2Memory &&
		iterations >= minArgon2Iterations && iterations <= maxArgon2Iterations &&
		parallelism >= minArgon2Parallelism && parallelism <= maxArgon2Parallelism
}

func validatePassword(password string) error {
	if len(password) < 12 {
		return errors.New("password must contain at least 12 characters")
	}
	if len(password) > 1024 {
		return errors.New("password is too long")
	}
	return nil
}
