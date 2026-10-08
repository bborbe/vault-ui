// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build race

package pageindex_test

// raceDetectorEnabled reports at compile time that the test binary was built
// with the race detector, whose runtime inflates allocation measurements.
const raceDetectorEnabled = true
