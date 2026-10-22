// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
)

// NewCountingPageStorage wraps a PageStorage so every file it reads increments
// vault_api_file_reads_total. The index build is the only caller that reads
// files; query handlers serve from the index and never reach this decorator,
// which is what makes a flat counter across a query window meaningful.
func NewCountingPageStorage(next storage.PageStorage, metrics Metrics) storage.PageStorage {
	return &countingPageStorage{next: next, metrics: metrics}
}

type countingPageStorage struct {
	next    storage.PageStorage
	metrics Metrics
}

func (c *countingPageStorage) ListPages(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]*domain.Page, error) {
	pages, err := c.next.ListPages(ctx, vaultPath, pagesDir)
	c.metrics.AddFileReads(FileReadKindList, int64(len(pages)))
	return pages, err
}

func (c *countingPageStorage) ReadPage(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	name string,
) (*domain.Page, error) {
	page, err := c.next.ReadPage(ctx, vaultPath, pagesDir, name)
	if err == nil {
		c.metrics.AddFileReads(FileReadKindRead, 1)
	}
	return page, err
}
