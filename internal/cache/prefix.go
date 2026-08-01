package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func HashStablePrefix(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		sum := sha256.Sum256([]byte("unhashable"))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
