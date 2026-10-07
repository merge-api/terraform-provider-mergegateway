// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// sweepPaths lists the collections the sweeper cleans up, in order. Each one
// lists as {"results": [{"id": ..., "name": ...}], ...} (keys: {"data":
// [{"hash": ..., "name": ...}]}) and deletes at <path>/<id>. Add a collection
// here when its resource gets acceptance tests. Keys come before projects, since
// a project with keys can't be deleted. Project settings (such as PI settings)
// need no entry: they go with their tf-acc- project.
var sweepPaths = []string{
	"/v1/keys",
	"/v1/projects",
	"/v1/routing-policies",
	"/v1/credentials",
}

// TestMain deletes tf-acc- resources left behind by crashed or cancelled
// runs before the suite starts. A failed sweep stops the suite: it means the
// API or the management key doesn't work, so every test would fail anyway.
func TestMain(m *testing.M) {
	if os.Getenv(resource.EnvTfAcc) != "" && os.Getenv(apiKeyEnvVar) != "" {
		if err := sweep(); err != nil {
			fmt.Fprintf(os.Stderr, "sweeper: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func sweep() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var errs []string
	for i, path := range sweepPaths {
		// The first collection proves the key works, so the rest may skip.
		if err := sweepCollection(ctx, path, i > 0); err != nil {
			errs = append(errs, path+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// sweepCollection deletes every item on the collection's first page whose name
// starts with the tf-acc- prefix; each run sweeps again, so one page is enough.
// Response bodies stay out of errors and logs, since some collections echo secrets.
// With mayBeMissing, a collection the API doesn't serve is skipped: one not
// deployed yet (dev answers an unrouted path with 401) or a feature not
// enabled for the test org (404).
func sweepCollection(ctx context.Context, path string, mayBeMissing bool) error {
	status, body, err := apiRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if mayBeMissing && (status == http.StatusNotFound || status == http.StatusUnauthorized) {
		fmt.Fprintf(os.Stderr, "sweeper: skipping %s: HTTP %d\n", path, status)
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, status)
	}

	type sweepItem struct {
		ID   string `json:"id"`
		Hash string `json:"hash"`
		Name string `json:"name"`
	}
	var list struct {
		Results []sweepItem `json:"results"`
		Data    []sweepItem `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("decode list: %w", err)
	}

	for _, item := range append(list.Results, list.Data...) {
		if item.ID == "" {
			item.ID = item.Hash
		}
		if !strings.HasPrefix(item.Name, namePrefix+"-") || item.ID == "" {
			continue
		}
		status, _, err := apiRequest(ctx, http.MethodDelete, path+"/"+item.ID, nil)
		if err != nil || (status >= 300 && status != http.StatusNotFound) {
			fmt.Fprintf(os.Stderr, "sweeper: failed deleting %s %q (%s): status %d, err %v\n", path, item.Name, item.ID, status, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "sweeper: deleted leftover %s %q\n", path, item.Name)
	}
	return nil
}
