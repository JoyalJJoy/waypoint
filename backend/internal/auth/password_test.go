package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	t.Parallel()

	for _, password := range []string{
		"Passw0rd!",
		"correct horse battery staple",
		"pässwörd-✓",
		strings.Repeat("a", 128),
	} {
		hash, err := HashPassword(password)
		if err != nil {
			t.Fatalf("HashPassword(%q): %v", password, err)
		}
		if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
			t.Fatalf("unexpected hash format: %s", hash)
		}
		if !VerifyPassword(password, hash) {
			t.Fatalf("VerifyPassword rejected the password used to create the hash")
		}
		if VerifyPassword(password+"x", hash) {
			t.Fatalf("VerifyPassword accepted a wrong password")
		}
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	t.Parallel()

	first, err := HashPassword("Passw0rd!")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword("Passw0rd!")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("identical passwords produced identical hashes; salt is not random")
	}
	if !VerifyPassword("Passw0rd!", first) || !VerifyPassword("Passw0rd!", second) {
		t.Fatal("hashes with different salts must both verify")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	t.Parallel()

	valid, err := HashPassword("Passw0rd!")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(valid, "$")

	cases := map[string]string{
		"empty":               "",
		"not argon2":          "$bcrypt$v=19$m=65536,t=3,p=2$c2FsdA$a2V5",
		"too few parts":       "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA",
		"wrong version":       "$argon2id$v=18$m=65536,t=3,p=2$" + parts[4] + "$" + parts[5],
		"zero memory":         "$argon2id$v=19$m=0,t=3,p=2$" + parts[4] + "$" + parts[5],
		"zero iterations":     "$argon2id$v=19$m=65536,t=0,p=2$" + parts[4] + "$" + parts[5],
		"bad salt encoding":   "$argon2id$v=19$m=65536,t=3,p=2$***$" + parts[5],
		"bad digest encoding": "$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$***",
	}
	for name, encoded := range cases {
		if VerifyPassword("Passw0rd!", encoded) {
			t.Errorf("%s: malformed hash was accepted", name)
		}
	}
}
