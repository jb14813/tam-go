package remote

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Retryable reports a status that means the server is unwell rather than
// the request being wrong, so the request is worth sending again later.
func (r *Response) Retryable() bool { return r.Status >= 500 }

// WithTimeout returns a client whose requests are cut off after d. It
// shares this client's connection pool.
func (c *Client) WithTimeout(d time.Duration) *Client {
	cp := *c
	hc := *c.http
	hc.Timeout = d
	cp.http = &hc
	return &cp
}

// NewPinned returns an HTTPS client that accepts only the server
// certificate whose SHA-256 fingerprint is fingerprint (lowercase hex, as
// returned by Fingerprint). This is trust-on-first-use: the fingerprint is
// recorded when the client is paired, and a changed certificate is refused
// until the client is paired again.
func NewPinned(baseURL, key, fingerprint string) *Client {
	c := New(baseURL, key, true)
	want := normalizeFingerprint(fingerprint)
	c.http.Transport.(*http.Transport).TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // the leaf is checked below instead
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("server sent no certificate")
			}
			if got := fingerprintOf(raw[0]); got != want {
				return fmt.Errorf("server certificate changed (fingerprint %s); pair with the server again", got)
			}
			return nil
		},
	}
	return c
}

// Fingerprint connects to hostPort over TLS, without verifying anything,
// and returns the SHA-256 fingerprint of the certificate it presents.
func Fingerprint(hostPort string) (string, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", hostPort, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // reading the certificate is the point
	if err != nil {
		return "", err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("server sent no certificate")
	}
	return fingerprintOf(certs[0].Raw), nil
}

func fingerprintOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeFingerprint(fp string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fp), ":", ""))
}
