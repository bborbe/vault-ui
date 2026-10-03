// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	"net/http"
	"time"

	libhttp "github.com/bborbe/http"
	"github.com/bborbe/log"
	"github.com/bborbe/run"
	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/handler"
)

// CreateReadiness returns the service readiness gate, which starts not-ready.
func CreateReadiness() vaultui.Readiness {
	return vaultui.NewReadiness()
}

// CreateHealthzHandler returns the canonical liveness handler.
func CreateHealthzHandler() http.Handler {
	return handler.NewHealthzHandler()
}

// CreateReadinessHandler returns the readiness handler backed by readiness.
func CreateReadinessHandler(readiness vaultui.Readiness) http.Handler {
	return handler.NewReadinessHandler(readiness)
}

// CreateHTTPServer returns a run.Func serving the canonical bborbe admin block.
func CreateHTTPServer(listen string, readiness vaultui.Readiness) run.Func {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		router := mux.NewRouter()
		router.Path("/healthz").Handler(CreateHealthzHandler())
		router.Path("/readiness").Handler(CreateReadinessHandler(readiness))
		router.Path("/metrics").Handler(promhttp.Handler())
		router.Path("/setloglevel/{level}").
			Handler(log.NewSetLoglevelHandler(ctx, log.NewLogLevelSetter(2, 5*time.Minute)))
		router.Path("/gc").Handler(libhttp.NewGarbageCollectorHandler())

		glog.V(2).Infof("starting http server listen on %s", listen)
		return libhttp.NewServer(listen, router).Run(ctx)
	}
}
