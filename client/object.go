package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// PutObject writes data to key, overwriting unconditionally — no lease CAS
// (no If-Match / If-None-Match). The hoplockserver stores arbitrary objects;
// the leader uses this to commit cluster state at "state/<cluster>" next to
// the election lease, giving hoplockserver-mode the same durable desired
// state that S3-mode already has.
//
// Single writer by contract: only the current leaseholder calls this, so the
// overwrite needs no conditional guard here (mirrors hoplock/s3's PutObject).
func (b *Backend) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	if b.URL == "" {
		return errors.New("hoplockserver/client: URL is required")
	}
	req, err := b.newObjectRequest(ctx, http.MethodPut, key, data)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return fmt.Errorf("hoplockserver/client: PUT %s: %w", key, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	default:
		return b.errForKey("PUT", key, resp)
	}
}

// GetObject reads key. ok is false when the object does not exist — which is
// not an error: an absent state object simply means a clean boot.
func (b *Backend) GetObject(ctx context.Context, key string) (data []byte, ok bool, err error) {
	if b.URL == "" {
		return nil, false, errors.New("hoplockserver/client: URL is required")
	}
	req, err := b.newObjectRequest(ctx, http.MethodGet, key, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("hoplockserver/client: GET %s: %w", key, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, b.errForKey("GET", key, resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("hoplockserver/client: read GET %s body: %w", key, err)
	}
	return body, true, nil
}
