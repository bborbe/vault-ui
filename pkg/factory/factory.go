// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	"net/http"
	_ "net/http/pprof" // registers the /debug/pprof/ handlers on http.DefaultServeMux
	"time"

	libhttp "github.com/bborbe/http"
	"github.com/bborbe/log"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
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

// CreateConfigLoader returns a vault-cli config loader. An empty configPath
// makes the loader use vault-cli's default config location.
func CreateConfigLoader(configPath string) config.Loader {
	return config.NewLoader(configPath)
}

// CreateOpSet builds the full vault-cli op set for a single vault. The
// injectable dependencies are parameters so callers and tests can substitute
// fakes for the session-spawning and publishing behaviour.
func CreateOpSet(
	vault *config.Vault,
	currentDateTime libtime.CurrentDateTime,
	publisher ops.EscalationPublisher,
	starter ops.ClaudeSessionStarter,
	resumer ops.ClaudeResumer,
	interactionCounter ops.InteractionCounter,
	uuidGenerator func() string,
) vaultui.OpSet {
	storageConfig := storage.NewConfigFromVault(vault)
	taskStore := storage.NewTaskStorage(storageConfig)
	goalStore := storage.NewGoalStorage(storageConfig)
	topicStore := storage.NewTopicStorage(storageConfig)
	dailyStore := storage.NewDailyNoteStorage(storageConfig)
	pageStore := storage.NewPageStorage(storageConfig)
	return vaultui.OpSet{
		List:             ops.NewListOperation(pageStore),
		Show:             ops.NewShowOperation(taskStore),
		FrontmatterSet:   ops.NewFrontmatterSetOperation(taskStore, currentDateTime, publisher, vault.Name, vault.GetTasksDir()),
		FrontmatterClear: ops.NewFrontmatterClearOperation(taskStore, publisher, vault.Name, vault.GetTasksDir()),
		WorkOn:           ops.NewWorkOnOperation(taskStore, dailyStore, currentDateTime, uuidGenerator, starter, resumer),
		Approve:          ops.NewTaskApproveOperation(taskStore, currentDateTime),
		Answer:           ops.NewTaskAnswerOperation(taskStore),
		Defer:            ops.NewDeferOperation(taskStore, dailyStore, currentDateTime),
		Complete:         ops.NewCompleteOperation(taskStore, dailyStore, currentDateTime, interactionCounter),
		GoalSet:          ops.NewGoalSetOperation(goalStore),
		GoalClear:        ops.NewGoalClearOperation(goalStore),
		GoalWorkOn:       ops.NewGoalWorkOnOperation(goalStore, uuidGenerator, starter, resumer),
		GoalDefer:        ops.NewGoalDeferOperation(goalStore, currentDateTime),
		GoalComplete:     ops.NewGoalCompleteOperation(goalStore, taskStore, currentDateTime),
		TopicShow:        ops.NewTopicShowOperation(topicStore),
		TopicSet:         ops.NewTopicSetOperation(topicStore),
		TopicClear:       ops.NewTopicClearOperation(topicStore),
	}
}

// CreateVaultDiscovery returns a run.Func that discovers the configured vaults
// once and marks readiness ready.
func CreateVaultDiscovery(loader config.Loader, readiness vaultui.Readiness) run.Func {
	return func(ctx context.Context) error {
		return vaultui.DiscoverVaults(ctx, loader, readiness)
	}
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
		// The Go runtime's own profiling handlers, on the admin port only —
		// never the board port. net/http/pprof registers itself on
		// http.DefaultServeMux, so mounting that mux under the canonical
		// prefix exposes /debug/pprof/{profile,heap,goroutine,trace,…} here
		// and nowhere else. This is the attribution tool: `sample` does not
		// resolve Go frames in this binary and the board had no profiler, so
		// a periodic CPU cost could be seen but not located.
		router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)
		router.Path("/setloglevel/{level}").
			Handler(log.NewSetLoglevelHandler(ctx, log.NewLogLevelSetter(2, 5*time.Minute)))
		router.Path("/gc").Handler(libhttp.NewGarbageCollectorHandler())

		glog.V(2).Infof("starting http server listen on %s", listen)
		return libhttp.NewServer(listen, router).Run(ctx)
	}
}
