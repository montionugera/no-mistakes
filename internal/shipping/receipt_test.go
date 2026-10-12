package shipping

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestReceiptSignatureAuthenticatesOriginalBytesAndDomain(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	key := ed25519.NewKeyFromSeed(seed)
	payload := []byte(`{"schema_version":1,"description":"<review & proof>"}`)
	envelope, err := Sign(payload, strings.NewReader(base64.StdEncoding.EncodeToString(seed)))
	if err != nil {
		t.Fatal(err)
	}
	var e Envelope
	if err := json.Unmarshal(envelope, &e); err != nil {
		t.Fatal(err)
	}
	bytes, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), append([]byte("no-mistakes-review-v1\x00"), bytes...), sig) {
		t.Fatal("signature mismatch")
	}
	bytes[0] = '['
	if ed25519.Verify(key.Public().(ed25519.PublicKey), append([]byte("no-mistakes-review-v1\x00"), bytes...), sig) {
		t.Fatal("tampered payload accepted")
	}
	if e.KeyID != Digest(key.Public().(ed25519.PublicKey)) {
		t.Fatal("key identity mismatch")
	}
}

func TestReceiptSigningRejectsSeedAndSizeWithoutLeakingInput(t *testing.T) {
	for _, key := range []string{"private-invalid-seed", base64.StdEncoding.EncodeToString(make([]byte, 64)), strings.Repeat("x", 1000)} {
		out, err := Sign([]byte(`{}`), strings.NewReader(key))
		if err == nil || len(out) != 0 {
			t.Fatal("invalid seed signed")
		}
		if strings.Contains(err.Error(), key) {
			t.Fatal("secret included in error")
		}
	}
	if _, err := Sign([]byte(strings.Repeat("x", MaxPayload+1)), strings.NewReader(base64.StdEncoding.EncodeToString(make([]byte, 32)))); err == nil {
		t.Fatal("oversize proof signed")
	}
}

func TestSharedGoReceiptFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/review-payload.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.TrimSpace(raw)
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	got, err := Sign(raw, strings.NewReader(base64.StdEncoding.EncodeToString(seed)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/review-envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.TrimSpace(want)) {
		t.Fatal("shared fixture differs from native signature")
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	findings, err := json.Marshal(p.Review.Findings)
	if err != nil {
		t.Fatal(err)
	}
	if Digest(findings) != p.Review.FindingsDigest {
		t.Fatal("fixture findings digest differs from emitted Go bytes")
	}
}
