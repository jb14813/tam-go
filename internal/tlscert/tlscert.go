// Package tlscert creates the self-signed certificate tam-server uses when
// started with -tls and no certificate is supplied, so remote mode can run
// over HTTPS without a reverse proxy. tam-client accepts self-signed
// certificates when its Remote TLS setting is on.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Files returns the certificate and key files tam-server -tls serves, from
// its -cert and -key flags, and whether it created them. Without either
// flag they are server.crt and server.key in dataDir, a self-signed pair
// for hosts created there on first use. Files the flags name must exist,
// and the flags go together: a new pair made at a path someone typed,
// perhaps with a typo, would be served in place of the certificate they
// meant. Either way the pair must load, so a broken one stops the start
// with the reason instead of failing every connection.
func Files(dataDir, cert, key string, hosts []string) (string, string, bool, error) {
	created := false
	switch {
	case cert == "" && key == "":
		cert, key = filepath.Join(dataDir, "server.crt"), filepath.Join(dataDir, "server.key")
		var err error
		if created, err = EnsurePair(cert, key, hosts); err != nil {
			return "", "", false, err
		}
	case cert == "":
		return "", "", false, errors.New("-key needs -cert: name both files, or neither for a self-signed pair in the data directory")
	case key == "":
		return "", "", false, errors.New("-cert needs -key: name both files, or neither for a self-signed pair in the data directory")
	default:
		for _, f := range []struct{ flag, path string }{{"-cert", cert}, {"-key", key}} {
			if _, err := os.Stat(f.path); errors.Is(err, fs.ErrNotExist) {
				return "", "", false, fmt.Errorf("%s %s: no such file (a self-signed pair is only made in the data directory, without -cert and -key)", f.flag, f.path)
			} else if err != nil {
				return "", "", false, fmt.Errorf("%s: %w", f.flag, err)
			}
		}
	}
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		return "", "", false, fmt.Errorf("certificate %s with key %s: %w", cert, key, err)
	}
	return cert, key, created, nil
}

// EnsurePair makes sure a certificate and key exist at the given paths,
// generating a self-signed pair valid for ten years when both are missing.
// hosts are the names and addresses the certificate is issued for. It
// reports whether a new pair was created.
func EnsurePair(certPath, keyPath string, hosts []string) (bool, error) {
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if certErr == nil && keyErr == nil {
		return false, nil
	}
	if !errors.Is(certErr, fs.ErrNotExist) || !errors.Is(keyErr, fs.ErrNotExist) {
		return false, fmt.Errorf("certificate files: only one of %s and %s exists or is readable", certPath, keyPath)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "tam-server", Organization: []string{"Ticket Auction Manager"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return false, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return false, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return false, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return false, err
	}
	return true, nil
}
