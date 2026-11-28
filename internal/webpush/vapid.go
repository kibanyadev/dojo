package webpush

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// ExampleKey is the fixed private key of .env.example; pre and pro refuse it (design 3.10).
const ExampleKey = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94" // gitleaks:allow

var enc = base64.RawURLEncoding

// VAPID signs RFC 8292 headers with one P-256 key.
type VAPID struct {
	key     *ecdsa.PrivateKey
	public  []byte
	example bool
}

// ParseVAPID reads a base64url P-256 private scalar (32 bytes).
func ParseVAPID(scalar string) (*VAPID, error) {
	raw, err := enc.DecodeString(scalar)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("webpush: VAPID key must be a base64url 32-byte P-256 scalar")
	}
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad VAPID key: %w", err)
	}
	priv, err := ecdh.P256().NewPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad VAPID key: %w", err)
	}
	return &VAPID{key: key, public: priv.PublicKey().Bytes(), example: scalar == ExampleKey}, nil
}

// PublicKey is the uncompressed point the browser subscribes with (65 bytes).
func (v *VAPID) PublicKey() []byte { return v.public }

// IsExample reports whether the key is the example key from .env.example.
func (v *VAPID) IsExample() bool { return v.example }

// Header returns the Authorization value for a push to the origin aud.
func (v *VAPID) Header(aud, sub string, now time.Time) (string, error) {
	claims, err := json.Marshal(map[string]any{"aud": aud, "exp": now.Add(12 * time.Hour).Unix(), "sub": sub})
	if err != nil {
		return "", err
	}
	signing := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	der, err := ecdsa.SignASN1(rand.Reader, v.key, sum[:])
	if err != nil {
		return "", err
	}
	var rs struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &rs); err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	rs.R.FillBytes(sig[:32])
	rs.S.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + enc.EncodeToString(sig) + ", k=" + enc.EncodeToString(v.public), nil
}
