// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"time"

	libhttp "github.com/bborbe/http"
	"github.com/bborbe/run"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"Sentry DSN"   display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

	Listen          string           `required:"false" arg:"listen"           env:"LISTEN"           usage:"HTTP listen address"                                                                                                                default:":9090"`
	VaultPath       string           `required:"true"  arg:"vault-path"       env:"VAULT_PATH"       usage:"path to the vault git working tree"`
	VaultName       string           `required:"true"  arg:"vault-name"       env:"VAULT_NAME"       usage:"vault name reported in responses"`
	InstanceStage   string           `required:"false" arg:"instance-stage"   env:"INSTANCE_STAGE"   usage:"stage of this vault-api instance, reported as the instance_stage metric label (dev, prod). Distinct from the per-task stage label." default:""`
	TasksDir        string           `required:"false" arg:"tasks-dir"        env:"TASKS_DIR"        usage:"task directory inside the vault"                                                                                                    default:"25 Tasks"`
	GoalsDir        string           `required:"false" arg:"goals-dir"        env:"GOALS_DIR"        usage:"goal directory inside the vault"                                                                                                    default:"24 Goals"`
	GatewaySecret   string           `required:"false" arg:"gateway-secret"   env:"GATEWAY_SECRET"   usage:"Shared secret required in X-Gateway-Secret for /api/v1/* requests. Empty = no auth."                                                                   display:"length"`
	RefreshInterval libtime.Duration `required:"false" arg:"refresh-interval" env:"REFRESH_INTERVAL" usage:"how often to check the working tree for a new HEAD"                                                                                 default:"30s"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	metrics := vaultapi.NewMetrics(a.InstanceStage, a.VaultName)

	storageConfig := storage.DefaultConfig()
	storageConfig.TasksDir = a.TasksDir
	storageConfig.GoalsDir = a.GoalsDir
	store := vaultapi.NewCountingPageStorage(storage.NewPageStorage(storageConfig), metrics)

	index := vaultapi.NewIndex(vaultapi.Config{
		VaultPath: a.VaultPath,
		VaultName: a.VaultName,
		TasksDir:  a.TasksDir,
		GoalsDir:  a.GoalsDir,
	}, metrics)
	if err := index.Build(ctx, store); err != nil {
		glog.Warningf("initial index build failed: %v", err)
	}

	return run.CancelOnFirstFinish(ctx,
		a.refresh(index, store),
		libhttp.NewServer(a.Listen, handler.NewHandler(ctx, index, a.GatewaySecret)),
	)
}

// refresh rebuilds the index when the working tree's HEAD changes, or while the
// first successful build has not happened yet. A steady HEAD means no file is
// read, so the file-read counter stays flat between pulls.
func (a *application) refresh(index *vaultapi.Index, store storage.PageStorage) run.Func {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(a.RefreshInterval.Duration())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				ready := index.Readiness().Ready
				if ready && index.Head() == vaultapi.ResolveHead(a.VaultPath) {
					continue
				}
				if err := index.Build(ctx, store); err != nil {
					glog.Warningf("index rebuild failed: %v", err)
				}
			}
		}
	}
}
