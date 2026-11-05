package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
)

func TestPinnedBytes(t *testing.T) {
	hash := sha256.Sum256([]byte(SQL()))
	if got := hex.EncodeToString(hash[:]); got != SHA256 {
		t.Fatalf("schema SHA256 = %s, want %s", got, SHA256)
	}
	for _, artifact := range Contracts() {
		t.Run(artifact.Name, func(t *testing.T) {
			data, err := ReadContract(artifact.Name)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(data)
			if got := hex.EncodeToString(hash[:]); got != artifact.SHA256 {
				t.Fatalf("contract SHA256 = %s, want %s", got, artifact.SHA256)
			}
			if artifact.Name[len(artifact.Name)-5:] == ".json" && !json.Valid(data) {
				t.Fatal("invalid JSON contract")
			}
			data[0] ^= 1
			again, _ := ReadContract(artifact.Name)
			if again[0] == data[0] {
				t.Fatal("mutable bytes escaped contract custody")
			}
		})
	}
	if _, err := ReadContract("../schema.sql"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unknown contract = %v, want not-exist", err)
	}
}
