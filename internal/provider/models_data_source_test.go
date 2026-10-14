package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

func TestModelsDataSourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &datasource.SchemaResponse{}
	NewModelsDataSource().Schema(ctx, datasource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

const testModelJSON = `{"id": "openai/gpt-4o", "provider": "openai", "display_name": "GPT-4o", "context_window": 128000,
	"max_output_tokens": 16384, "input_modalities": ["text", "image"], "output_modalities": ["text"],
	"input_cost_per_million": 2.5, "output_cost_per_million": 10, "status": "available",
	"expiration_date": "2027-03-31", "replacement_model": "openai/gpt-5"}`

func TestModelFromAPI(t *testing.T) {
	var m client.ModelV1Response
	if err := json.Unmarshal([]byte(testModelJSON), &m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got, diags := modelFromAPI(t.Context(), m)
	if diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	want := modelModel{
		ID:                   types.StringValue("openai/gpt-4o"),
		ModelProvider:        types.StringValue("openai"),
		DisplayName:          types.StringValue("GPT-4o"),
		ContextWindow:        types.Int64Value(128000),
		MaxOutputTokens:      types.Int64Value(16384),
		InputModalities:      testModels("text", "image"),
		OutputModalities:     testModels("text"),
		InputCostPerMillion:  types.Float64Value(2.5),
		OutputCostPerMillion: types.Float64Value(10),
		Status:               types.StringValue("available"),
		ExpirationDate:       types.StringValue("2027-03-31"),
		ReplacementModel:     types.StringValue("openai/gpt-5"),
	}
	if !got.InputModalities.Equal(want.InputModalities) || !got.OutputModalities.Equal(want.OutputModalities) {
		t.Fatalf("modalities = %v, %v", got.InputModalities, got.OutputModalities)
	}
	got.InputModalities, got.OutputModalities = want.InputModalities, want.OutputModalities
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestModelsDataSourceRead reads two pages from a stub API, passing the
// filters and the cursor along.
func TestModelsDataSourceRead(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organization/models" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"results": [` + testModelJSON + `], "next": "page-2", "previous": null}`))
			return
		}
		_, _ = w.Write([]byte(`{"results": [{"id": "anthropic/claude-sonnet", "provider": "anthropic", "display_name": "Claude",
			"context_window": 200000, "max_output_tokens": 64000, "input_modalities": ["text"], "output_modalities": ["text"],
			"input_cost_per_million": 3, "output_cost_per_million": 15, "status": "beta", "expiration_date": null,
			"replacement_model": null}], "next": null, "previous": null}`))
	}))
	defer server.Close()

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"mergegateway": providerserver.NewProtocol6WithError(New("test")()),
		},
		Steps: []tfresource.TestStep{{
			Config: `
provider "mergegateway" {
  api_key  = "test"
  base_url = "` + server.URL + `"
}
data "mergegateway_models" "test" {
  min_context_length = 100000
  input_modality     = "text"
}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.mergegateway_models.test", tfjsonpath.New("models"), knownvalue.ListSizeExact(2)),
				statecheck.ExpectKnownValue("data.mergegateway_models.test", tfjsonpath.New("models").AtSliceIndex(1).AtMapKey("model_provider"),
					knownvalue.StringExact("anthropic")),
				statecheck.ExpectKnownValue("data.mergegateway_models.test", tfjsonpath.New("models").AtSliceIndex(1).AtMapKey("expiration_date"),
					knownvalue.Null()),
			},
		}},
	})

	if len(queries) < 2 {
		t.Fatalf("got %d requests, want at least 2 (two pages)", len(queries))
	}
	want := url.Values{"min_context_length": {"100000"}, "input_modality": {"text"}, "limit": {"100"}}
	if got, _ := url.ParseQuery(queries[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("first query = %v, want %v", got, want)
	}
	want.Set("cursor", "page-2")
	if got, _ := url.ParseQuery(queries[1]); !reflect.DeepEqual(got, want) {
		t.Fatalf("second query = %v, want %v", got, want)
	}
}
