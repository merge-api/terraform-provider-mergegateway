package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ datasource.DataSource              = &VendorsDataSource{}
	_ datasource.DataSourceWithConfigure = &VendorsDataSource{}
)

func NewVendorsDataSource() datasource.DataSource {
	return &VendorsDataSource{}
}

// VendorsDataSource lists the active vendors through /v1/organization/vendors.
type VendorsDataSource struct {
	client *client.ClientWithResponses
}

// vendorsDataSourceModel describes the data source data model.
type vendorsDataSourceModel struct {
	Vendors []vendorModel `tfsdk:"vendors"`
}

// vendorModel is one vendor.
type vendorModel struct {
	Slug        types.String `tfsdk:"slug"`
	DisplayName types.String `tfsdk:"display_name"`
	VendorType  types.String `tfsdk:"vendor_type"`
	AllowBYOK   types.Bool   `tfsdk:"allow_byok"`
	CountryCode types.String `tfsdk:"country_code"`
	GeoRegion   types.String `tfsdk:"geo_region"`
}

func (d *VendorsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vendors"
}

func (d *VendorsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the active vendors: the APIs the Gateway can send requests to, such as a model creator's " +
			"own API or a cloud host.",
		Attributes: map[string]schema.Attribute{
			"vendors": schema.ListNestedAttribute{
				MarkdownDescription: "The vendors, in the API's order.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"slug": schema.StringAttribute{
							MarkdownDescription: "The vendor's ID, such as `bedrock`: the value `mergegateway_credential.vendor` and the vendor lists take.",
							Computed:            true,
						},
						"display_name": schema.StringAttribute{MarkdownDescription: "The vendor's name for display.", Computed: true},
						"vendor_type": schema.StringAttribute{
							MarkdownDescription: "`direct` for the model creator's own API, `hosted` for a cloud host.",
							Computed:            true,
						},
						"allow_byok": schema.BoolAttribute{
							MarkdownDescription: "Whether the organization can add its own credential for it with `mergegateway_credential`.",
							Computed:            true,
						},
						"country_code": schema.StringAttribute{
							MarkdownDescription: "The ISO 3166-1 alpha-2 code of its home country.",
							Computed:            true,
						},
						"geo_region": schema.StringAttribute{
							MarkdownDescription: "The region the region controls (`allowed_regions`, `ignored_regions`) know it by.",
							Computed:            true,
						},
					},
				},
			},
		},
	}
}

func (d *VendorsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.ClientWithResponses)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.ClientWithResponses, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	d.client = c
}

func (d *VendorsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	limit := maxPageSize
	params := client.ListVendorsParams{Limit: &limit}
	data := vendorsDataSourceModel{Vendors: []vendorModel{}}
	for {
		page, err := d.client.ListVendorsWithResponse(ctx, &params)
		if err != nil {
			resp.Diagnostics.AddError("Unable to list vendors", err.Error())
			return
		}
		if page.JSON200 == nil {
			resp.Diagnostics.AddError("Unable to list vendors", apiError(page.StatusCode(), page.Body))
			return
		}
		for _, v := range page.JSON200.Results {
			data.Vendors = append(data.Vendors, vendorFromAPI(v))
		}
		next, err := page.JSON200.Next.Get()
		if err != nil {
			break
		}
		params.Cursor = &next
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// vendorFromAPI maps an API vendor to the Terraform model.
func vendorFromAPI(v client.VendorV1Response) vendorModel {
	return vendorModel{
		Slug:        types.StringValue(v.Slug),
		DisplayName: types.StringValue(v.DisplayName),
		VendorType:  types.StringValue(string(v.VendorType)),
		AllowBYOK:   types.BoolValue(v.AllowByok),
		CountryCode: nullableString(v.CountryCode),
		GeoRegion:   nullableString(v.GeoRegion),
	}
}
