// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gitrest is the write half of the API. It hands a fully rendered file
// to the vault's git-rest service, which commits and pushes it as the vault's
// own git identity. The API never touches git itself and the vault volume stays
// mounted read-only.
package gitrest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bborbe/errors"
)

// HeaderGatewaySecret is the header git-rest reads the shared secret from.
const HeaderGatewaySecret = "X-Gateway-Secret"

// HeaderGatewayInitator is the caller-identity header git-rest's auth middleware
// requires to be non-empty — it answers 500 when the header is missing, before it
// ever checks the secret. The misspelling (missing the second "i") is deliberate
// and part of git-rest's frozen public contract, so it is reproduced verbatim.
const HeaderGatewayInitator = "X-Gateway-Initator"

// Initiator is the value this client sends in HeaderGatewayInitator. git-rest
// only checks that the header is non-empty; the value is what its request log
// records as the caller.
const Initiator = "vault-api"

// Client writes one complete file through git-rest.
type Client interface {
	// WriteFile replaces path with content. git-rest answers {"ok":true} and no
	// SHA, so the resulting commit is identified afterwards from git log, never
	// from this call's response.
	WriteFile(ctx context.Context, path string, content []byte) error
}

// StatusError is a non-2xx answer from git-rest. The handler maps it to the
// status the caller should see, so a refusal upstream is not reported as a
// failure of this API.
type StatusError struct {
	StatusCode int
	Body       string
}

// Error returns the git-rest status and the body it answered with.
func (e *StatusError) Error() string {
	return fmt.Sprintf("git-rest answered %d: %s", e.StatusCode, e.Body)
}

// NewClient returns a Client that posts to baseURL. gatewaySecret is sent as
// X-Gateway-Secret; an empty secret sends no header, matching a git-rest
// instance started without one.
func NewClient(baseURL string, gatewaySecret string, httpClient *http.Client) Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &client{
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		gatewaySecret: gatewaySecret,
		httpClient:    httpClient,
	}
}

type client struct {
	baseURL       string
	gatewaySecret string
	httpClient    *http.Client
}

// WriteFile posts the complete file to git-rest.
func (c *client) WriteFile(ctx context.Context, path string, content []byte) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/v1/files/"+encodePath(path),
		bytes.NewReader(content),
	)
	if err != nil {
		return errors.Wrap(ctx, err, "build git-rest request")
	}
	request.Header.Set("Content-Type", "text/markdown; charset=utf-8")
	// git-rest refuses the request outright when this is missing, so it is set
	// unconditionally — it is caller identity, not a credential.
	request.Header.Set(HeaderGatewayInitator, Initiator)
	if c.gatewaySecret != "" {
		request.Header.Set(HeaderGatewaySecret, c.gatewaySecret)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return errors.Wrap(ctx, err, "write file through git-rest")
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return errors.Wrap(ctx, &StatusError{
			StatusCode: response.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}, "write file through git-rest")
	}
	// The body is {"ok":true}; it carries no SHA and nothing here depends on it.
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// encodePath escapes each segment of a vault-relative path while keeping the
// separators, so git-rest sees the same directory structure it would read from
// disk.
func encodePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
