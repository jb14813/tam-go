// Package remote is the HTTP client tam-client uses to talk to a tam-server.
package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ticket-auction-manager/tam-go/internal/store"
	"ticket-auction-manager/tam-go/internal/version"
)

// Client talks to one remote server with one access key.
type Client struct {
	base string
	key  string
	http *http.Client
}

// New returns a client for baseURL (for example https://tam.lan:8443). When
// insecureTLS is set the server certificate is not verified. Paired clients
// use NewPinned to verify their trusted server certificate.
//
// Connecting is given five seconds, so an unreachable server fails fast; a
// whole request is given thirty, so a large backup push over slow Wi-Fi is
// not cut off.
func New(baseURL, key string, insecureTLS bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 5 * time.Second
	tr.ResponseHeaderTimeout = 10 * time.Second
	if insecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // caller explicitly selected unverified TLS
	}
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		key:  key,
		http: &http.Client{Timeout: 30 * time.Second, Transport: tr},
	}
}

// WithKey returns a client that sends key and shares this client's
// connection pool, so a changed access key does not open new sockets.
func (c *Client) WithKey(key string) *Client {
	cp := *c
	cp.key = key
	return &cp
}

// Response is what the remote server answered.
type Response struct {
	Status  int
	Body    []byte
	Header  http.Header
	Receipt *store.SaveReceipt
}

// OK reports a 2xx status.
func (r *Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// JSON decodes the body into v.
func (r *Response) JSON(v any) error { return json.Unmarshal(r.Body, v) }

// Do sends a request with the access key and an X-TAM-Client header naming
// this program and its version, which the server's admin page shows. body,
// when not nil, is sent as JSON. A transport failure is returned as an
// error; any HTTP status is returned as a Response.
func (c *Client) Do(method, path string, headers map[string]string, body any) (*Response, error) {
	return c.DoContext(context.Background(), method, path, headers, body)
}

// DoContext sends a request within the caller's operation deadline. Multiple
// requests sharing a context consume one budget, including connection setup.
func (c *Client) DoContext(ctx context.Context, method, path string, headers map[string]string, body any) (*Response, error) {
	var rdr io.Reader
	var requestBody []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		requestBody = data
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("TAM-KEY", c.key)
	req.Header.Set("X-TAM-Client", "tam-client/"+version.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	out := &Response{Status: res.StatusCode, Body: data, Header: res.Header.Clone()}
	wantsReceipt := headers["X-TAM-Save"] != "" || (headers["X-TAM-Receipts"] == "1" && method == http.MethodPost)
	if res.Header.Get("X-TAM-Receipts") == "1" || (out.OK() && wantsReceipt) {
		var envelope struct {
			Data    json.RawMessage    `json:"data"`
			Receipt *store.SaveReceipt `json:"receipt"`
			Client  string             `json:"client"`
			Save    int64              `json:"save"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Data) == 0 || envelope.Receipt == nil || envelope.Receipt.Revisions == nil {
			return nil, fmt.Errorf("server sent an invalid save receipt")
		}
		if number := headers["X-TAM-Save"]; number != "" {
			n, err := strconv.ParseInt(number, 10, 64)
			if err != nil || n <= 0 || envelope.Save != n || envelope.Client != headers["X-TAM-Client-Name"] {
				return nil, fmt.Errorf("server sent a receipt for a different save")
			}
			if err := store.ValidateSaveReceipt(store.Outbox{Method: method, Path: path, Body: requestBody, Order: store.Order{Client: envelope.Client, Save: n}}, envelope.Receipt); err != nil {
				return nil, fmt.Errorf("server sent an invalid save receipt: %w", err)
			}
		}
		out.Body, out.Receipt = envelope.Data, envelope.Receipt
	}
	return out, nil
}

// Get sends a GET.
func (c *Client) Get(path string) (*Response, error) { return c.Do(http.MethodGet, path, nil, nil) }

// Post sends a JSON POST.
func (c *Client) Post(path string, body any) (*Response, error) {
	return c.Do(http.MethodPost, path, nil, body)
}

// Delete sends a DELETE.
func (c *Client) Delete(path string) (*Response, error) {
	return c.Do(http.MethodDelete, path, nil, nil)
}
