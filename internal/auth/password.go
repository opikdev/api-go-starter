// Package auth hashes passwords and session tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id settings from the OWASP password storage cheat sheet.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
	tokenBytes   = 32
)

var encoding = base64.RawStdEncoding

// semaphore runs at most cap(s) functions at once.
type semaphore chan struct{}

func (s semaphore) run(f func()) {
	s <- struct{}{}
	defer func() { <-s }()
	f()
}

// argonSlots caps concurrent hashes, because each one allocates argonMemory KiB.
var argonSlots = make(semaphore, runtime.GOMAXPROCS(0))

func idKey(password, salt []byte, time, memory uint32, threads uint8) []byte {
	var key []byte
	argonSlots.run(func() { key = argon2.IDKey(password, salt, time, memory, threads, argonKeyLen) })
	return key
}

// HashPassword returns an argon2id hash in PHC string format.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := idKey([]byte(password), salt, argonTime, argonMemory, argonThreads)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, encoding.EncodeToString(salt), encoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a hash from HashPassword, in constant time.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("stored password hash has an unknown format")
	}
	var version int
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("stored password hash has an unsupported version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errors.New("stored password hash has invalid parameters")
	}
	salt, err := encoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("stored password hash has an invalid salt")
	}
	expected, err := encoding.DecodeString(parts[5])
	if err != nil || len(expected) != argonKeyLen {
		return false, errors.New("stored password hash has an invalid key")
	}
	actual := idKey([]byte(password), salt, time, memory, threads)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

// NewToken returns a random session token for the client, and the hash to store.
func NewToken() (token, hash string, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken returns the stored form of a session token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
