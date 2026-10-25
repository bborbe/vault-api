// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gitrest

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bborbe/errors"
)

// ErrClaimConflict is returned when git-rest's compare-and-swap rejects a claim
// ref write because another holder won the race. It is a benign, expected
// outcome — the caller asked for a claim someone else holds — so handlers map
// it to 409 rather than reporting it as a failure of this API.
var ErrClaimConflict = stderrors.New("claim ref conflict")

// ClaimRef is one claim ref as git-rest reports it. The field names are
// git-rest's, reproduced verbatim: this type is the wire shape, not the typed
// API's vocabulary.
type ClaimRef struct {
	Ref        string `json:"ref"`
	SHA        string `json:"sha"`
	Cluster    string `json:"cluster"`
	TaskID     string `json:"task_id"`
	LeaseUntil string `json:"lease_until"`
}

// ClaimRefRequest is the create/renew body. An empty ExpectedSHA means the ref
// must not exist yet — that is git-rest's compare-and-swap, and it is what makes
// exactly one of several concurrent acquirers win.
type ClaimRefRequest struct {
	ExpectedSHA string `json:"expected_sha"`
	Cluster     string `json:"cluster"`
	TaskID      string `json:"task_id"`
	LeaseUntil  string `json:"lease_until"`
}

// RefClient reads and writes claim refs through git-rest. It is separate from
// Client because the two talk to different routes and answer different errors:
// a file write either lands or fails, while a claim write has a third outcome —
// it lost the race — that callers must be able to distinguish.
type RefClient interface {
	// PutClaimRef creates or renews the claim at key. Returns ErrClaimConflict
	// when the compare-and-swap loses.
	PutClaimRef(ctx context.Context, key string, request ClaimRefRequest) (ClaimRef, error)
	// ListClaimRefs returns every claim ref under prefix, read live from the
	// origin.
	ListClaimRefs(ctx context.Context, prefix string) ([]ClaimRef, error)
	// DeleteClaimRef releases the claim at key. expectedSHA is mandatory and is
	// the compare-and-swap token; a stale one returns ErrClaimConflict.
	DeleteClaimRef(ctx context.Context, key string, expectedSHA string) error
}

// NewRefClient returns a RefClient that talks to baseURL. It shares the gateway
// headers with NewClient: git-rest refuses a request that carries no initiator,
// and checks the secret only when one is configured.
func NewRefClient(baseURL string, gatewaySecret string, httpClient *http.Client) RefClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &client{
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		gatewaySecret: gatewaySecret,
		httpClient:    httpClient,
	}
}

// PutClaimRef creates or renews the claim at key.
func (c *client) PutClaimRef(
	ctx context.Context,
	key string,
	request ClaimRefRequest,
) (ClaimRef, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return ClaimRef{}, errors.Wrap(ctx, err, "marshal claim ref request")
	}
	httpRequest, err := c.newRequest(ctx, http.MethodPut, "/api/v1/refs/"+encodePath(key), body)
	if err != nil {
		return ClaimRef{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	var answer struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, httpRequest, &answer, "put claim ref"); err != nil {
		return ClaimRef{}, err
	}
	return ClaimRef{
		Ref:        answer.Ref,
		SHA:        answer.SHA,
		Cluster:    request.Cluster,
		TaskID:     request.TaskID,
		LeaseUntil: request.LeaseUntil,
	}, nil
}

// ListClaimRefs returns every claim ref under prefix.
func (c *client) ListClaimRefs(ctx context.Context, prefix string) ([]ClaimRef, error) {
	target := "/api/v1/refs?prefix=" + url.QueryEscape(prefix)
	httpRequest, err := c.newRequest(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	var refs []ClaimRef
	if err := c.do(ctx, httpRequest, &refs, "list claim refs"); err != nil {
		return nil, err
	}
	return refs, nil
}

// DeleteClaimRef releases the claim at key.
func (c *client) DeleteClaimRef(ctx context.Context, key string, expectedSHA string) error {
	target := "/api/v1/refs/" + encodePath(key) + "?expected_sha=" + url.QueryEscape(expectedSHA)
	httpRequest, err := c.newRequest(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	return c.do(ctx, httpRequest, nil, "delete claim ref")
}

// newRequest builds a request carrying the headers git-rest requires.
func (c *client) newRequest(
	ctx context.Context,
	method string,
	target string,
	body []byte,
) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+target, reader)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "build git-rest request")
	}
	// git-rest answers 500 when this is missing, before it checks the secret, so
	// it is set unconditionally — it is caller identity, not a credential.
	request.Header.Set(HeaderGatewayInitator, Initiator)
	if c.gatewaySecret != "" {
		request.Header.Set(HeaderGatewaySecret, c.gatewaySecret)
	}
	return request, nil
}

// do sends the request and decodes a 2xx body into answer when answer is not
// nil. A 409 becomes ErrClaimConflict; any other non-2xx becomes a StatusError.
func (c *client) do(
	ctx context.Context,
	request *http.Request,
	answer interface{},
	message string,
) error {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return errors.Wrap(ctx, err, message)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		statusErr := &StatusError{
			StatusCode: response.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}
		if response.StatusCode == http.StatusConflict {
			return errors.Wrapf(ctx, ErrClaimConflict, "%s: %s", message, statusErr.Error())
		}
		return errors.Wrap(ctx, statusErr, message)
	}
	if answer == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(answer); err != nil {
		return errors.Wrap(ctx, err, message+": decode response")
	}
	return nil
}
