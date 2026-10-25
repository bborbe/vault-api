// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gitrest_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-api/pkg/gitrest"
)

var _ = Describe("RefClient", func() {
	var (
		ctx           context.Context
		server        *httptest.Server
		client        gitrest.RefClient
		methodSeen    string
		pathSeen      string
		querySeen     string
		secretSeen    string
		initiatorSeen string
		bodySeen      string
		status        int
		answer        string
	)

	BeforeEach(func() {
		ctx = context.Background()
		status = http.StatusOK
		answer = `{"ref":"refs/claims/task/abc","sha":"deadbeef"}`
		server = httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				methodSeen = r.Method
				pathSeen = r.URL.EscapedPath()
				querySeen = r.URL.RawQuery
				secretSeen = r.Header.Get(gitrest.HeaderGatewaySecret)
				initiatorSeen = r.Header.Get(gitrest.HeaderGatewayInitator)
				body, _ := io.ReadAll(r.Body)
				bodySeen = string(body)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(answer))
			},
		))
		client = gitrest.NewRefClient(server.URL, "shared-secret", nil)
	})

	AfterEach(func() {
		server.Close()
	})

	Describe("PutClaimRef", func() {
		It("puts the claim to the key path with the transport body", func() {
			ref, err := client.PutClaimRef(ctx, "claims/task/abc", gitrest.ClaimRefRequest{
				Cluster:    "nukedev",
				TaskID:     "abc",
				LeaseUntil: "2026-10-25T11:55:00Z",
			})
			Expect(err).To(BeNil())
			Expect(methodSeen).To(Equal(http.MethodPut))
			Expect(pathSeen).To(Equal("/api/v1/refs/claims/task/abc"))
			Expect(secretSeen).To(Equal("shared-secret"))
			Expect(initiatorSeen).To(Equal(gitrest.Initiator))
			Expect(bodySeen).To(ContainSubstring(`"cluster":"nukedev"`))
			Expect(bodySeen).To(ContainSubstring(`"task_id":"abc"`))
			Expect(bodySeen).To(ContainSubstring(`"lease_until":"2026-10-25T11:55:00Z"`))
			Expect(ref.Ref).To(Equal("refs/claims/task/abc"))
			Expect(ref.SHA).To(Equal("deadbeef"))
			// The lease fields are echoed from the request: git-rest answers only
			// the ref and the sha, so the caller cannot otherwise learn them.
			Expect(ref.Cluster).To(Equal("nukedev"))
			Expect(ref.LeaseUntil).To(Equal("2026-10-25T11:55:00Z"))
		})

		It("reports a lost compare-and-swap as ErrClaimConflict", func() {
			status = http.StatusConflict
			answer = `{"code":"CLAIM_CONFLICT","message":"held by sun"}`
			_, err := client.PutClaimRef(ctx, "claims/task/abc", gitrest.ClaimRefRequest{
				Cluster:    "nukedev",
				TaskID:     "abc",
				LeaseUntil: "2026-10-25T11:55:00Z",
			})
			Expect(err).NotTo(BeNil())
			Expect(errors.Is(err, gitrest.ErrClaimConflict)).To(BeTrue())
		})

		It("reports any other non-2xx answer as a StatusError", func() {
			status = http.StatusBadRequest
			answer = `{"message":"cluster must not be empty"}`
			_, err := client.PutClaimRef(ctx, "claims/task/abc", gitrest.ClaimRefRequest{})
			Expect(err).NotTo(BeNil())
			var statusErr *gitrest.StatusError
			Expect(errors.As(err, &statusErr)).To(BeTrue())
			Expect(statusErr.StatusCode).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("ListClaimRefs", func() {
		It("lists by prefix and decodes the refs", func() {
			answer = `[{"ref":"refs/claims/task/abc","sha":"deadbeef",` +
				`"cluster":"nukedev","task_id":"abc","lease_until":"2026-10-25T11:55:00Z"}]`
			refs, err := client.ListClaimRefs(ctx, "claims/task/")
			Expect(err).To(BeNil())
			Expect(methodSeen).To(Equal(http.MethodGet))
			Expect(pathSeen).To(Equal("/api/v1/refs"))
			Expect(querySeen).To(Equal("prefix=claims%2Ftask%2F"))
			Expect(refs).To(HaveLen(1))
			Expect(refs[0].TaskID).To(Equal("abc"))
			Expect(refs[0].Cluster).To(Equal("nukedev"))
		})

		It("returns an empty slice when git-rest reports no refs", func() {
			answer = `null`
			refs, err := client.ListClaimRefs(ctx, "claims/task/")
			Expect(err).To(BeNil())
			Expect(refs).To(BeEmpty())
		})
	})

	Describe("DeleteClaimRef", func() {
		It("deletes with the expected sha as the compare-and-swap token", func() {
			answer = `{"ok":true}`
			Expect(client.DeleteClaimRef(ctx, "claims/task/abc", "deadbeef")).To(Succeed())
			Expect(methodSeen).To(Equal(http.MethodDelete))
			Expect(pathSeen).To(Equal("/api/v1/refs/claims/task/abc"))
			Expect(querySeen).To(Equal("expected_sha=deadbeef"))
		})

		It("reports a stale token as ErrClaimConflict", func() {
			status = http.StatusConflict
			answer = `{"code":"CLAIM_CONFLICT"}`
			err := client.DeleteClaimRef(ctx, "claims/task/abc", "stale")
			Expect(err).NotTo(BeNil())
			Expect(errors.Is(err, gitrest.ErrClaimConflict)).To(BeTrue())
		})
	})
})
