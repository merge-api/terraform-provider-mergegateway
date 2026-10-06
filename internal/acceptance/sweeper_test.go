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

// sweepPaths lists the collections the sweeper cleans up. Each one lists as
// {"results": [{"id": ..., "name": ...}], ...} and deletes at <path>/<id>.
// Add a collection here when its resource gets acceptance tests.
var sweepPaths = []string{
	"/v1/projects",
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
	for _, path := range sweepPaths {
		if err := sweepCollection(ctx, path); err != nil {
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
func sweepCollection(ctx context.Context, path string) error {
	status, body, err := apiRequest(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", path, status)
	}

	var list struct {
		Results []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("decode list: %w", err)
	}

	for _, item := range list.Results {
		if !strings.HasPrefix(item.Name, namePrefix+"-") || item.ID == "" {
			continue
		}
		status, _, err := apiRequest(ctx, http.MethodDelete, path+"/"+item.ID)
		if err != nil || (status >= 300 && status != http.StatusNotFound) {
			fmt.Fprintf(os.Stderr, "sweeper: failed deleting %s %q (%s): status %d, err %v\n", path, item.Name, item.ID, status, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "sweeper: deleted leftover %s %q\n", path, item.Name)
	}
	return nil
}
