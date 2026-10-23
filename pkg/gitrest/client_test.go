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

var _ = Describe("Client", func() {
	var (
		ctx           context.Context
		server        *httptest.Server
		requestedAt   string
		secretSeen    string
		initiatorSeen string
		bodySeen      string
		status        int
	)

	BeforeEach(func() {
		ctx = context.Background()
		status = http.StatusOK
		server = httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				requestedAt = r.URL.EscapedPath()
				secretSeen = r.Header.Get(gitrest.HeaderGatewaySecret)
				initiatorSeen = r.Header.Get(gitrest.HeaderGatewayInitator)
				body, _ := io.ReadAll(r.Body)
				bodySeen = string(body)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"ok":true}`))
			},
		))
	})

	AfterEach(func() {
		server.Close()
	})

	It("posts the whole file to the escaped path with the gateway secret", func() {
		client := gitrest.NewClient(server.URL+"/", "shared-secret", server.Client())
		Expect(client.WriteFile(
			ctx, "25 Tasks/Alpha Task.md", []byte("---\nphase: planning\n---\n"),
		)).To(Succeed())

		Expect(requestedAt).To(Equal("/api/v1/files/25%20Tasks/Alpha%20Task.md"))
		Expect(secretSeen).To(Equal("shared-secret"))
		// git-rest answers 500 before it checks the secret when this is absent,
		// so the header is part of the contract, not an optional extra.
		Expect(initiatorSeen).To(Equal(gitrest.Initiator))
		Expect(bodySeen).To(Equal("---\nphase: planning\n---\n"))
	})

	It("sends no secret header when none is configured", func() {
		client := gitrest.NewClient(server.URL, "", server.Client())
		Expect(client.WriteFile(ctx, "Alpha.md", []byte("x"))).To(Succeed())
		Expect(secretSeen).To(BeEmpty())
	})

	It("reports a non-2xx answer as a StatusError", func() {
		status = http.StatusConflict
		client := gitrest.NewClient(server.URL, "", server.Client())
		err := client.WriteFile(ctx, "Alpha.md", []byte("x"))

		Expect(err).NotTo(BeNil())
		var statusErr *gitrest.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue())
		Expect(statusErr.StatusCode).To(Equal(http.StatusConflict))
	})
})
