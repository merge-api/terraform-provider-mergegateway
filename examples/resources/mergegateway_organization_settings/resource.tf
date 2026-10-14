# The settings of the organization the management key belongs to. Settings
# left out keep their current values. Destroying this resource leaves the
# settings as they are.
resource "mergegateway_organization_settings" "this" {
  log_payloads        = false
  zero_data_retention = true
  web_search_mode     = "auto"

  tag_definitions = [
    { key = "team", values = ["support", "sales"] },
  ]

  vendor_restrictions = {
    allowed_vendors           = ["openai", "anthropic"]
    vendor_routing_preference = ["anthropic", "openai"]
  }
}
