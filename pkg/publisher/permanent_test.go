// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package publisher_test

import (
	"errors"

	"github.com/IBM/sarama"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-api/pkg/publisher"
)

var _ = Describe("IsPermanent", func() {
	It("treats a client-side oversized message as permanent", func() {
		// sarama rejects a message over Producer.MaxMessageBytes before the broker
		// ever sees it, so the error is a ConfigurationError rather than a KError.
		// Classifying it transient means the retry budget resets on every tick and
		// the feed never reaches the cap that abandons a poison message.
		Expect(publisher.IsPermanent(sarama.ConfigurationError("message too large"))).To(BeTrue())
	})

	It("treats a broker-side oversized message as permanent", func() {
		Expect(publisher.IsPermanent(sarama.ErrMessageSizeTooLarge)).To(BeTrue())
	})

	It("keeps a connection failure transient", func() {
		// The whole point of the distinction: an outage must not consume the budget
		// meant for a message the broker will never accept.
		Expect(publisher.IsPermanent(errors.New("dial tcp: connection refused"))).To(BeFalse())
	})

	It("keeps a nil error transient rather than classifying it", func() {
		Expect(publisher.IsPermanent(nil)).To(BeFalse())
	})
})
