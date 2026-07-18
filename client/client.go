// Package client implements hoplock.Backend against a hoplockserver
// instance.
//
// This is the lightweight counterpart to hoplock/s3: same lease semantics,
// no AWS account, no sigv4. Authentication is a single shared X-API-Key
// header.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/xinix00/hoplock"
)

// Backend talks to a hoplockserver over HTTP. The zero value is not
// usable; populate URL and Key, then pass to hoplock.Elector or use the
// Backend methods directly.
type Backend struct {
	// URL is the base URL of the hoplockserver (e.g. http://lock:8090).
	// Required.
	URL string

	// Key is the object key for the lease record. Required.
	Key string

	// APIKey is sent as X-API-Key. Optional; omit when the server has
	// authentication disabled.
	APIKey string

	// HTTPClient is used for all requests. Defaults to http.DefaultClient.
	HTTPClient *http.Client
}

var _ hoplock.Backend = (*Backend)(nil)

// Read fetches the lease object. The server's ETag is returned as the
// hoplock.Backend handle.
func (b *Backend) Read(ctx context.Context) (*hoplock.State, string, error) {
	if err := b.validate(); err != nil {
		return nil, "", err
	}
	req, err := b.newRequest(ctx, http.MethodGet, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("hoplockserver/client: GET %s: %w", b.Key, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, "", hoplock.ErrNoLease
	default:
		return nil, "", b.errFromResponse("GET", resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("hoplockserver/client: read GET body: %w", err)
	}
	var state hoplock.State
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, "", fmt.Errorf("hoplockserver/client: decode lease: %w", err)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return nil, "", errors.New("hoplockserver/client: GET response missing ETag")
	}
	return &state, etag, nil
}

// Write commits state. An empty prevHandle creates the object only if it
// does not exist; otherwise prevHandle is used as If-Match. Returns
// hoplock.ErrLeaseHeld on 412 PreconditionFailed.
func (b *Backend) Write(ctx context.Context, prevHandle string, state *hoplock.State) (string, error) {
	if err := b.validate(); err != nil {
		return "", err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("hoplockserver/client: marshal state: %w", err)
	}
	req, err := b.newRequest(ctx, http.MethodPut, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if prevHandle == "" {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", prevHandle)
	}

	resp, err := b.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("hoplockserver/client: PUT %s: %w", b.Key, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusPreconditionFailed, http.StatusConflict:
		return "", hoplock.ErrLeaseHeld
	default:
		return "", b.errFromResponse("PUT", resp)
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", errors.New("hoplockserver/client: PUT response missing ETag")
	}
	return etag, nil
}

// Delete removes the lease iff handle still matches the server's ETag.
func (b *Backend) Delete(ctx context.Context, handle string) error {
	if err := b.validate(); err != nil {
		return err
	}
	if handle == "" {
		return hoplock.ErrLeaseHeld
	}
	req, err := b.newRequest(ctx, http.MethodDelete, nil)
	if err != nil {
		return err
	}
	req.Header.Set("If-Match", handle)

	resp, err := b.client().Do(req)
	if err != nil {
		return fmt.Errorf("hoplockserver/client: DELETE %s: %w", b.Key, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return hoplock.ErrNoLease
	case http.StatusPreconditionFailed:
		return hoplock.ErrLeaseHeld
	default:
		return b.errFromResponse("DELETE", resp)
	}
}

func (b *Backend) validate() error {
	if b.URL == "" {
		return errors.New("hoplockserver/client: URL is required")
	}
	if b.Key == "" {
		return errors.New("hoplockserver/client: Key is required")
	}
	return nil
}

func (b *Backend) newRequest(ctx context.Context, method string, body []byte) (*http.Request, error) {
	url := strings.TrimRight(b.URL, "/") + "/" + strings.TrimLeft(b.Key, "/")
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("hoplockserver/client: build %s: %w", method, err)
	}
	if b.APIKey != "" {
		req.Header.Set("X-API-Key", b.APIKey)
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	return req, nil
}

func (b *Backend) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return http.DefaultClient
}

func (b *Backend) errFromResponse(op string, resp *http.Response) error {
	const maxBody = 4 << 10
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return fmt.Errorf("hoplockserver/client: %s %s: status %d %s: %s",
		op, b.Key, resp.StatusCode, http.StatusText(resp.StatusCode), strings.TrimSpace(string(body)))
}
