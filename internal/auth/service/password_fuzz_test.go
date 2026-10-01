package service_test

import (
	"testing"

	"github.com/AlexandrKhromov2005/vault-chat/internal/auth/service"
)

// FuzzArgon2idHasher_Verify feeds random passwords and PHC strings to Verify.
// It must never panic and must return an error for malformed hashes.
func FuzzArgon2idHasher_Verify(f *testing.F) {
	hasher := service.NewArgon2idHasher(fastParams)

	valid, err := hasher.Hash("password1")
	if err != nil {
		f.Fatalf("failed to build seed hash: %v", err)
	}

	f.Add("password1", valid)
	f.Add("", "")
	f.Add("x", "garbage")
	f.Add("password1", "$argon2id$v=19$m=1024,t=1,p=2$!!!!$!!!!")

	f.Fuzz(func(t *testing.T, password, hash string) {
		if len(password) > 1024 || len(hash) > 4096 {
			t.Skip("input too large")
		}

		match, err := hasher.Verify(password, hash)
		if err != nil && match {
			t.Fatalf("Verify returned match=true together with an error")
		}
	})
}
