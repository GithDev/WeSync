package certid

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// generateSelfSigned returns a self-signed TLS certificate using the same
// approach as Syncthing: ECDSA P-384.
func generateSelfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "syncthing"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        cert,
	}
}

func TestRoundTrip(t *testing.T) {
	cert := generateSelfSigned(t)
	der := cert.Certificate[0]

	id := DeviceIDFromCert(der)
	if len(id) != 63 { // 56 chars + 7 hyphens
		t.Fatalf("DeviceID length = %d, want 63: %q", len(id), id)
	}

	ok, err := MatchesCert(id, der)
	if err != nil {
		t.Fatalf("MatchesCert: %v", err)
	}
	if !ok {
		t.Error("MatchesCert returned false for matching cert")
	}
}

func TestMismatch(t *testing.T) {
	a := generateSelfSigned(t)
	b := generateSelfSigned(t)

	idA := DeviceIDFromCert(a.Certificate[0])
	ok, err := MatchesCert(idA, b.Certificate[0])
	if err != nil {
		t.Fatalf("MatchesCert: %v", err)
	}
	if ok {
		t.Error("MatchesCert returned true for different cert")
	}
}

func TestHashFromDeviceID_InvalidLength(t *testing.T) {
	_, err := HashFromDeviceID("tooshort")
	if err == nil {
		t.Error("expected error for short DeviceID")
	}
}
