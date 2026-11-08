package auth

import (
	"strings"
	"testing"
)

func TestPasswords(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(hash, "correct-horse") {
		t.Fatalf("hash = %s", hash)
	}
	if ok, err := VerifyPassword("correct-horse-battery", hash); !ok || err != nil {
		t.Fatalf("right password: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong-horse-battery", hash); ok {
		t.Fatal("wrong password accepted")
	}
	other, _ := HashPassword("correct-horse-battery")
	if other == hash {
		t.Fatal("hashes aren't salted")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, hash := range []string{"bcrypt$abc", "$argon2id$v=18$m=1,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$bad$c2FsdA$a2V5", "$argon2id$v=19$m=1,t=1,p=1$!!$a2V5", "$argon2id$v=19$m=1,t=1,p=1$c2FsdA$!!"} {
		if _, err := VerifyPassword("x", hash); err == nil {
			t.Errorf("accepted %q", hash)
		}
	}
}

func TestTokens(t *testing.T) {
	token, hash, err := NewToken()
	if err != nil || len(token) < 40 || hash != HashToken(token) || hash == token {
		t.Fatalf("token %q hash %q err %v", token, hash, err)
	}
	other, _, _ := NewToken()
	if other == token {
		t.Fatal("tokens repeat")
	}
}
