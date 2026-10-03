// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/factory"
)

// adminListen is the canonical bborbe admin port. It is deliberately NOT a
// flag or env var — spec 021 Non-goals forbid a port knob.
const adminListen = ":9090"

func main() {
	defer glog.Flush()
	ctx := run.ContextWithSig(context.Background())
	if err := execute(ctx); err != nil {
		glog.Errorf("vault-ui failed: %v", err)
		glog.Flush()
		os.Exit(1)
	}
}

func execute(ctx context.Context) error {
	readiness := factory.CreateReadiness()
	loader := factory.CreateConfigLoader("")
	if err := run.CancelOnFirstErrorWait(ctx,
		factory.CreateVaultDiscovery(loader, readiness),
		factory.CreateHTTPServer(adminListen, readiness),
	); err != nil {
		return errors.Wrap(ctx, err, "run failed")
	}
	return nil
}
