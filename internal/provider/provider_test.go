// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// testAccProtoV6ProviderFactories is used to instantiate a provider during acceptance testing.
// The factory function is called for each Terraform CLI command to create a provider
// server that the CLI can connect to and interact with.
//
//nolint:unused // Used by the acceptance tests that come with each resource.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"mergegateway": providerserver.NewProtocol6WithError(New("test")()),
}

//nolint:unused // Used by the acceptance tests that come with each resource.
func testAccPreCheck(t *testing.T) {
	if v := os.Getenv(apiKeyEnvVar); v == "" {
		t.Fatal(apiKeyEnvVar + " must be set for acceptance tests")
	}
}

func TestProviderSchema(t *testing.T) {
	ctx := t.Context()
	resp := &provider.SchemaResponse{}
	New("test")().Schema(ctx, provider.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
	if !resp.Schema.Attributes["api_key"].IsSensitive() {
		t.Fatal("api_key must be sensitive")
	}
}

// configure runs the provider's Configure with the given api_key and base_url values.
func configure(t *testing.T, apiKey, baseURL tftypes.Value) *provider.ConfigureResponse {
	t.Helper()

	ctx := t.Context()
	p := New("test")()
	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)

	config := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
			"api_key":  apiKey,
			"base_url": baseURL,
		}),
	}
	resp := &provider.ConfigureResponse{}
	p.Configure(ctx, provider.ConfigureRequest{Config: config}, resp)
	return resp
}

func TestProviderConfigure(t *testing.T) {
	tests := map[string]struct {
		apiKey    tftypes.Value
		baseURL   tftypes.Value
		env       string
		expectErr bool
	}{
		"api_key from config": {
			apiKey:  tftypes.NewValue(tftypes.String, "config-key"),
			baseURL: tftypes.NewValue(tftypes.String, nil),
		},
		"api_key from env": {
			apiKey:  tftypes.NewValue(tftypes.String, nil),
			baseURL: tftypes.NewValue(tftypes.String, nil),
			env:     "env-key",
		},
		"custom base_url": {
			apiKey:  tftypes.NewValue(tftypes.String, "config-key"),
			baseURL: tftypes.NewValue(tftypes.String, "http://localhost:8080"),
		},
		"empty api_key falls back to env": {
			apiKey:  tftypes.NewValue(tftypes.String, ""),
			baseURL: tftypes.NewValue(tftypes.String, nil),
			env:     "env-key",
		},
		"empty base_url uses default": {
			apiKey:  tftypes.NewValue(tftypes.String, "config-key"),
			baseURL: tftypes.NewValue(tftypes.String, ""),
		},
		"base_url without scheme": {
			apiKey:    tftypes.NewValue(tftypes.String, "config-key"),
			baseURL:   tftypes.NewValue(tftypes.String, "api-gateway.merge.dev"),
			expectErr: true,
		},
		"missing api_key": {
			apiKey:    tftypes.NewValue(tftypes.String, nil),
			baseURL:   tftypes.NewValue(tftypes.String, nil),
			expectErr: true,
		},
		"unknown api_key": {
			apiKey:    tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			baseURL:   tftypes.NewValue(tftypes.String, nil),
			expectErr: true,
		},
		"unknown base_url": {
			apiKey:    tftypes.NewValue(tftypes.String, "config-key"),
			baseURL:   tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			expectErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(apiKeyEnvVar, tt.env)
			resp := configure(t, tt.apiKey, tt.baseURL)

			if tt.expectErr {
				if !resp.Diagnostics.HasError() {
					t.Fatal("expected an error")
				}
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected error: %v", resp.Diagnostics)
			}
			if _, ok := resp.ResourceData.(*client.ClientWithResponses); !ok {
				t.Fatalf("ResourceData is %T, want *client.ClientWithResponses", resp.ResourceData)
			}
		})
	}
}

func TestProviderClientSendsAPIKey(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	t.Setenv(apiKeyEnvVar, "env-key")
	resp := configure(t, tftypes.NewValue(tftypes.String, nil), tftypes.NewValue(tftypes.String, server.URL))
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}

	c, ok := resp.ResourceData.(*client.ClientWithResponses)
	if !ok {
		t.Fatalf("ResourceData is %T, want *client.ClientWithResponses", resp.ResourceData)
	}
	if _, err := c.ListProjectsWithResponse(t.Context()); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if gotAuth != "Bearer env-key" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer env-key")
	}
}
