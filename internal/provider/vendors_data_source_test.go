// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

func TestVendorsDataSourceSchema(t *testing.T) {
	ctx := t.Context()
	resp := &datasource.SchemaResponse{}
	NewVendorsDataSource().Schema(ctx, datasource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

func TestVendorFromAPI(t *testing.T) {
	var v client.VendorV1Response
	if err := json.Unmarshal([]byte(`{"slug": "bedrock", "display_name": "Amazon Bedrock", "vendor_type": "hosted",
		"allow_byok": true, "country_code": null, "geo_region": "us"}`), &v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := vendorModel{
		Slug:        types.StringValue("bedrock"),
		DisplayName: types.StringValue("Amazon Bedrock"),
		VendorType:  types.StringValue("hosted"),
		AllowBYOK:   types.BoolValue(true),
		CountryCode: types.StringNull(),
		GeoRegion:   types.StringValue("us"),
	}
	if got := vendorFromAPI(v); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
