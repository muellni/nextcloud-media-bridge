package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

type MediaRef struct {
	Path     string `json:"path"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

var ErrInvalidMediaID = errors.New("invalid media id")

func EncodeMediaID(secret []byte, ref MediaRef) (string, error) {
	payload, err := json.Marshal(ref)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := signMediaID(secret, encoded)
	return encoded + "_" + signature, nil
}

func DecodeMediaID(secret []byte, mediaID string) (MediaRef, error) {
	parts := strings.SplitN(mediaID, "_", 2)
	if len(parts) != 2 {
		return MediaRef{}, ErrInvalidMediaID
	}
	if !hmac.Equal([]byte(signMediaID(secret, parts[0])), []byte(parts[1])) {
		return MediaRef{}, ErrInvalidMediaID
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return MediaRef{}, ErrInvalidMediaID
	}
	var ref MediaRef
	if err := json.Unmarshal(payload, &ref); err != nil {
		return MediaRef{}, ErrInvalidMediaID
	}
	if ref.Path == "" {
		return MediaRef{}, ErrInvalidMediaID
	}
	return ref, nil
}

func signMediaID(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SignBytes returns a base64url-encoded HMAC-SHA256 signature for the payload.
func SignBytes(secret []byte, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyBytes verifies a base64url-encoded HMAC-SHA256 signature for the payload.
func VerifyBytes(secret []byte, payload []byte, signature string) bool {
	return hmac.Equal([]byte(SignBytes(secret, payload)), []byte(signature))
}
