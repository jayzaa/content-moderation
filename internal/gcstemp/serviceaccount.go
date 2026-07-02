package gcstemp

import "encoding/json"

// serviceAccountKey mirrors the fields we need from a GCP service account
// JSON key file. We parse it ourselves (rather than relying on internals of
// the storage/oauth2 packages) because SignedURLOptions requires the raw
// GoogleAccessID (client_email) and PrivateKey (PEM) explicitly.
type serviceAccountKey struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// extractClientEmail parses the service account email out of a JSON key file.
func extractClientEmail(keyData []byte) (string, error) {
	var k serviceAccountKey
	if err := json.Unmarshal(keyData, &k); err != nil {
		return "", err
	}
	return k.ClientEmail, nil
}

// extractPrivateKeyPEM parses the PEM-encoded private key out of a JSON key
// file. Returns nil if parsing fails; callers should already have called
// extractClientEmail (via New) to validate the file, so this only fails if
// the JSON is malformed in a way the first parse didn't catch.
func extractPrivateKeyPEM(keyData []byte) []byte {
	var k serviceAccountKey
	if err := json.Unmarshal(keyData, &k); err != nil {
		return nil
	}
	return []byte(k.PrivateKey)
}
