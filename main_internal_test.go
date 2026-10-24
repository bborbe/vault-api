// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("eventVaultName", func() {
	It("prefers the routing slug when one is configured", func() {
		app := &application{VaultName: "private-personal", EventVaultName: "personal"}

		Expect(app.eventVaultName()).To(Equal("personal"))
	})

	It("falls back to the reported vault name when no slug is configured", func() {
		app := &application{VaultName: "private-personal"}

		Expect(app.eventVaultName()).To(Equal("private-personal"))
	})

	It("treats an explicitly empty slug as unset rather than stamping an empty vault", func() {
		app := &application{VaultName: "personal", EventVaultName: ""}

		Expect(app.eventVaultName()).To(Equal("personal"))
	})
})
