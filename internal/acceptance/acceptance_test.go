// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

// Package acceptance holds the provider's acceptance tests and their shared
// helpers. The tests create real resources, so they only run when TF_ACC=1
// (the terraform-plugin-testing convention); otherwise they skip.
//
// Environment:
//
//	TF_ACC=1
//	MERGE_GATEWAY_MANAGEMENT_KEY  a management key for a dedicated test org
//	                              (CI uses one for a test org on dev; locally,
//	                              scripts/seed-acceptance.sh mints one)
//	MERGE_GATEWAY_BASE_URL        optional; defaults to the local Docker stack
//	                              at http://localhost:8001
package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/merge-api/terraform-provider-mergegateway/internal/provider"
)

const (
	apiKeyEnvVar   = "MERGE_GATEWAY_MANAGEMENT_KEY"
	baseURLEnvVar  = "MERGE_GATEWAY_BASE_URL"
	defaultBaseURL = "http://localhost:8001"

	// namePrefix marks every resource the tests create, so the sweeper can
	// find and delete leftovers from crashed runs.
	namePrefix = "tf-acc"
)

// protoV6ProviderFactories starts an in-process provider for each Terraform command.
func protoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"mergegateway": providerserver.NewProtocol6WithError(provider.New("test")()),
	}
}

func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv(apiKeyEnvVar) == "" {
		t.Fatal(apiKeyEnvVar + " must be set for acceptance tests")
	}
}

func baseURL() string {
	if u := os.Getenv(baseURLEnvVar); u != "" {
		return u
	}
	return defaultBaseURL
}

// providerConfig renders the provider block. The management key is not
// written into the config: the provider reads it from MERGE_GATEWAY_MANAGEMENT_KEY.
func providerConfig() string {
	return fmt.Sprintf(`
provider "mergegateway" {
  base_url = %q
}
`, baseURL())
}

// randomName returns a unique name such as "tf-acc-4823094712" for a test resource.
func randomName() string {
	return acctest.RandomWithPrefix(namePrefix)
}

// checkDestroy verifies against the API that every resourceType in state is
// really gone after destroy: GET <path>/<id> must return 404. Response bodies
// are never read or logged, since some resources echo secrets.
func checkDestroy(resourceType, path string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		for _, rs := range s.RootModule().Resources {
			if rs.Type != resourceType {
				continue
			}
			status, _, err := apiRequest(ctx, http.MethodGet, path+"/"+rs.Primary.ID, nil)
			if err != nil {
				return fmt.Errorf("checking %s %s was destroyed: %w", resourceType, rs.Primary.ID, err)
			}
			if status != http.StatusNotFound {
				return fmt.Errorf("%s %s still exists after destroy: GET %s/%s returned HTTP %d",
					resourceType, rs.Primary.ID, path, rs.Primary.ID, status)
			}
		}
		return nil
	}
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// apiRequest calls the Merge Gateway API directly with the test management key,
// for checks, cleanup and drift that must not go through the provider. body,
// if not nil, is sent as JSON.
func apiRequest(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, baseURL()+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv(apiKeyEnvVar))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}
