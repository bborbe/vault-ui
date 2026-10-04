// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package websocket_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/websocket"
)

var _ = Describe("Prometheus metrics", func() {
	It("records every live-channel event without panicking", func() {
		metrics := websocket.NewMetrics()
		Expect(func() {
			metrics.ClientConnected()
			metrics.Broadcast()
			metrics.ClientDropped()
			metrics.ClientDisconnected()
		}).NotTo(Panic())
	})
})
