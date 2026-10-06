// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/merge-api/terraform-provider-mergegateway/internal/client"
)

// The routing policy rules shared by mergegateway_project's routing_policy
// block and mergegateway_routing_policy, which take the same policy fields.

// priorityOrderModel is one priority_order entry.
type priorityOrderModel struct {
	Model    types.String `tfsdk:"model"`
	Priority types.Int64  `tfsdk:"priority"`
}

var priorityOrderAttrTypes = map[string]attr.Type{
	"model":    types.StringType,
	"priority": types.Int64Type,
}

// validateRoutingPolicy reports policy configs the API would reject: a
// strategy without its models, fields that don't apply to the strategy, and a
// model listed twice in priority_order. Values not known yet are skipped.
// policyPath is where the policy's attributes live.
func validateRoutingPolicy(ctx context.Context, policyPath path.Path, strategy, axis types.String, allowedModels types.List, priorityOrder types.Set) diag.Diagnostics {
	var diags diag.Diagnostics

	switch strategy.ValueString() {
	case string(client.ProjectPolicyV1StrategyPRIORITY):
		if priorityOrder.IsNull() {
			diags.AddAttributeError(policyPath.AtName("priority_order"), "Missing priority_order",
				"A PRIORITY routing policy needs priority_order.")
		}
		if !allowedModels.IsNull() && !allowedModels.IsUnknown() {
			diags.AddAttributeError(policyPath.AtName("allowed_models"), "Invalid routing policy",
				"allowed_models applies only to INTELLIGENT routing policies.")
		}
		if !axis.IsNull() && !axis.IsUnknown() {
			diags.AddAttributeError(policyPath.AtName("axis"), "Invalid routing policy",
				"axis applies only to INTELLIGENT routing policies.")
		}
	case string(client.ProjectPolicyV1StrategyINTELLIGENT):
		if allowedModels.IsNull() {
			diags.AddAttributeError(policyPath.AtName("allowed_models"), "Missing allowed_models",
				"An INTELLIGENT routing policy needs allowed_models.")
		}
		if !priorityOrder.IsNull() && !priorityOrder.IsUnknown() {
			diags.AddAttributeError(policyPath.AtName("priority_order"), "Invalid routing policy",
				"priority_order applies only to PRIORITY routing policies.")
		}
	}

	// Two entries for one model are distinct set elements when their priorities differ.
	if !priorityOrder.IsNull() && !priorityOrder.IsUnknown() {
		var entries []priorityOrderModel
		diags.Append(priorityOrder.ElementsAs(ctx, &entries, true)...)
		seen := map[string]bool{}
		for _, e := range entries {
			if e.Model.IsUnknown() {
				continue
			}
			if seen[e.Model.ValueString()] {
				diags.AddAttributeError(policyPath.AtName("priority_order"), "Duplicate model in priority_order",
					fmt.Sprintf("%q is listed more than once.", e.Model.ValueString()))
			}
			seen[e.Model.ValueString()] = true
		}
	}
	return diags
}

// checkDroppedModels reports models the API dropped from an INTELLIGENT
// policy's allowed_models (it drops models with no capability tier), instead
// of letting Terraform fail with an inconsistent result. attrPath is the
// allowed_models attribute; planned and got are its value in the plan and in
// the API's response.
func checkDroppedModels(attrPath path.Path, planned, got types.List) diag.Diagnostics {
	var diags diag.Diagnostics
	if planned.IsNull() || planned.Equal(got) {
		return diags
	}

	kept := map[string]bool{}
	for _, v := range got.Elements() {
		if s, ok := v.(types.String); ok {
			kept[s.ValueString()] = true
		}
	}
	var dropped []string
	for _, v := range planned.Elements() {
		if s, ok := v.(types.String); ok && !kept[s.ValueString()] {
			dropped = append(dropped, s.ValueString())
		}
	}
	diags.AddAttributeError(attrPath,
		"Models dropped from allowed_models",
		fmt.Sprintf("Merge Gateway dropped these models from the routing policy because they have no capability tier: %s. "+
			"Remove them from %s.", strings.Join(dropped, ", "), attrPath))
	return diags
}
