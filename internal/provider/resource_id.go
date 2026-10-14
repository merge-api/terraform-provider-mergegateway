package provider

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// parseUUID parses the UUID in a resource's id attribute. what names the
// resource in the error, such as "credential".
func parseUUID(id types.String, what string) (uuid.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	parsed, err := uuid.Parse(id.ValueString())
	if err != nil {
		diags.AddAttributeError(path.Root("id"), fmt.Sprintf("Invalid %s ID", what),
			fmt.Sprintf("Expected a %s UUID, got %q: %s", what, id.ValueString(), err))
	}
	return parsed, diags
}
