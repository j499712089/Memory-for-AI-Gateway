package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
)

// SHA256 computes the SHA256 hash of the input string
func SHA256(input string) string {
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// SHA256Bytes computes the SHA256 hash of the input bytes
func SHA256Bytes(input []byte) string {
	hash := sha256.Sum256(input)
	return hex.EncodeToString(hash[:])
}
