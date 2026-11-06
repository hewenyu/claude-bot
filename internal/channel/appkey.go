package channel

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashedAppKey is the app key of a channel called name whose credential
// does not say which app it is: a hash of it, so that the key, which is
// stored and shown, never holds the secret itself.
func HashedAppKey(name, credential string) string {
	sum := sha256.Sum256([]byte(credential))

	return name + ":sha256-" + hex.EncodeToString(sum[:8])
}
