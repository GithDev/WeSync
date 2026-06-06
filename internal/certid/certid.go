// Package certid converts between Syncthing DeviceIDs and TLS certificate hashes.
//
// A Syncthing DeviceID is the SHA-256 hash of a certificate's DER bytes,
// base32-encoded with four Luhn check characters interleaved, grouped as 8×7
// with hyphens. This package lets us verify that a presented certificate
// actually belongs to the DeviceID it claims.
package certid

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/base32"
	"fmt"
	"strings"
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// Load reads an existing TLS keypair from disk. It never generates one —
// WeSync's peerwire identity must be Syncthing's own cert so the wire device ID
// matches the ST device ID. Returns an error if the files are missing/invalid;
// callers must treat that as fatal rather than substituting another identity.
func Load(certFile, keyFile string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("certid: load cert: %w", err)
	}
	return &cert, nil
}

// HashFromDeviceID extracts the 32-byte SHA-256 hash encoded in a DeviceID.
// The four Luhn check characters (at positions 13, 27, 41, 55 of the
// hyphen-stripped string) are removed before decoding.
func HashFromDeviceID(deviceID string) ([32]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(deviceID, "-", ""))
	if len(s) != 56 {
		return [32]byte{}, fmt.Errorf("certid: invalid DeviceID length %d (want 56 without hyphens)", len(s))
	}
	// Strip the four Luhn check chars at positions 13, 27, 41, 55.
	data := s[:13] + s[14:27] + s[28:41] + s[42:55]
	// 52 base32 chars + 4 padding = 56; decodes to 35 bytes (32 meaningful + 3 zero-pad).
	decoded, err := base32.StdEncoding.DecodeString(data + "====")
	if err != nil {
		return [32]byte{}, fmt.Errorf("certid: base32 decode: %w", err)
	}
	if len(decoded) < 32 {
		return [32]byte{}, fmt.Errorf("certid: decoded too short: %d", len(decoded))
	}
	var h [32]byte
	copy(h[:], decoded[:32])
	return h, nil
}

// CertHash returns the SHA-256 hash of a DER-encoded certificate — the same
// value that Syncthing uses as the basis for a DeviceID.
func CertHash(certDER []byte) [32]byte {
	return sha256.Sum256(certDER)
}

// MatchesCert reports whether deviceID corresponds to the given DER certificate.
func MatchesCert(deviceID string, certDER []byte) (bool, error) {
	expected, err := HashFromDeviceID(deviceID)
	if err != nil {
		return false, err
	}
	return CertHash(certDER) == expected, nil
}

// DeviceIDFromCert derives the Syncthing DeviceID string from a DER certificate.
func DeviceIDFromCert(certDER []byte) string {
	hash := sha256.Sum256(certDER)
	b32 := strings.TrimRight(base32.StdEncoding.EncodeToString(hash[:]), "=")
	b32 = luhnify(b32)
	groups := make([]string, 8)
	for i := range groups {
		groups[i] = b32[i*7 : (i+1)*7]
	}
	return strings.Join(groups, "-")
}

// luhnify inserts a Luhn-32 check character after every 13 data characters.
func luhnify(s string) string {
	var b strings.Builder
	for i := 0; i < 4; i++ {
		chunk := s[i*13 : (i+1)*13]
		b.WriteString(chunk)
		b.WriteByte(luhn32(chunk))
	}
	return b.String()
}

func luhn32(s string) byte {
	factor := 1
	sum := 0
	for i := len(s) - 1; i >= 0; i-- {
		codePoint := strings.IndexByte(alphabet, s[i])
		addend := factor * codePoint
		if factor == 2 {
			factor = 1
		} else {
			factor = 2
		}
		addend = (addend / 32) + (addend % 32)
		sum += addend
	}
	remainder := sum % 32
	check := (32 - remainder) % 32
	return alphabet[check]
}
