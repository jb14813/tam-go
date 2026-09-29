package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

var ErrUnauthenticated = errors.New("server rejected this client's access key")

// NativeStatus is the authenticated capability response required before
// delivering event data. Missing fields never imply an older safe protocol.
type NativeStatus struct {
	Whoami         string  `json:"whoami"`
	Authenticated  bool    `json:"authenticated"`
	Healthy        bool    `json:"healthy"`
	BackupMetadata bool    `json:"backup_metadata"`
	Receipts       bool    `json:"receipts"`
	RecoveryToken  string  `json:"recovery_token"`
	Conflicts      *int    `json:"conflicts"`
	ReviewToken    *string `json:"review_token"`
}

func (r *Response) NativeStatus() (NativeStatus, error) {
	var status NativeStatus
	if r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden {
		return status, ErrUnauthenticated
	}
	if !r.OK() || r.JSON(&status) != nil || status.Whoami != "TAM Server" || !status.Healthy {
		return status, fmt.Errorf("server did not return a supported native capability response")
	}
	if !status.Authenticated {
		return status, ErrUnauthenticated
	}
	if !status.BackupMetadata || !status.Receipts || status.Conflicts == nil || *status.Conflicts < 0 || status.ReviewToken == nil {
		return status, fmt.Errorf("server does not support the required native backup and receipt protocol")
	}
	return status, nil
}

// Handshake verifies this destination and access key before a direct write,
// including the first write after manually entering connection settings.
func (c *Client) Handshake(client string) (*Response, error) {
	return c.HandshakeContext(context.Background(), client)
}

// HandshakeContext checks capabilities within the deadline shared with the
// operation that will follow, so validation cannot extend a page's save wait.
func (c *Client) HandshakeContext(ctx context.Context, client string) (*Response, error) {
	res, err := c.DoContext(ctx, http.MethodGet, "/api", map[string]string{"X-TAM-Client-Name": client}, nil)
	if err != nil {
		return nil, err
	}
	_, err = res.NativeStatus()
	if errors.Is(err, ErrUnauthenticated) {
		return &Response{Status: http.StatusUnauthorized, Body: []byte(`{"detail":"Invalid Key"}`)}, nil
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}
