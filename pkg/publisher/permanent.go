// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package publisher

import (
	stderrors "errors"

	"github.com/IBM/sarama"
)

// IsPermanent reports whether a publish error can never succeed on retry because
// the broker rejected the message itself rather than the connection.
//
// The distinction is load-bearing for the pull path: a caller that retries
// transient failures forever needs to know which failures are not transient, and
// the alternative — counting every failure toward an abandonment cap — drops real
// changes during any outage longer than a few ticks.
//
// A sarama ConfigurationError is permanent too, and it is the case that matters
// most: the producer rejects a message over Producer.MaxMessageBytes locally,
// before the broker ever sees it, so the error is not a KError and would
// otherwise be classified transient. One oversized task body would then block
// every later change forever, because the retry budget is reset on each tick and
// never reaches the cap that exists to abandon a poison message.
func IsPermanent(err error) bool {
	var brokerErr sarama.KError
	if stderrors.As(err, &brokerErr) {
		switch brokerErr {
		case sarama.ErrMessageSizeTooLarge,
			sarama.ErrInvalidMessage,
			sarama.ErrInvalidMessageSize:
			return true
		}
	}
	var configErr sarama.ConfigurationError
	return stderrors.As(err, &configErr)
}
