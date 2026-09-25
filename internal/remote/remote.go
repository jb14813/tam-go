// Package remote is the HTTP client tam-client uses to talk to a tam-server.
package remote

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to one remote server with one access key.
type Client struct {
	base string
	key  string
	http *http.Client
}

// New returns a client for baseURL (for example https://tam.lan:8443). When
// insecureTLS is set the server certificate is not verified, which matches
// the original's policy for the self-signed Caddy certificate.
func New(baseURL, key string, insecureTLS bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // mirrors the original deployment
	}
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		key:  key,
		http: &http.Client{Timeout: 5 * time.Second, Transport: tr},
	}
}

// Response is what the remote server answered.
type Response struct {
	Status int
	Body   []byte
}

// OK reports a 2xx status.
func (r *Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// JSON decodes the body into v.
func (r *Response) JSON(v any) error { return json.Unmarshal(r.Body, v) }

// Do sends a request with the access key. body, when not nil, is sent as
// JSON. A transport failure is returned as an error; any HTTP status is
// returned as a Response.
func (c *Client) Do(method, path string, headers map[string]string, body any) (*Response, error) {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("TAM-KEY", c.key)
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
	return &Response{Status: res.StatusCode, Body: data}, nil
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
