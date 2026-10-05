// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package panecache_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
)

func TestPanecache(t *testing.T) {
	format.TruncatedDiff = false
	RegisterFailHandler(Fail)
	RunSpecs(t, "Panecache Suite")
}
