resource "mergegateway_project" "support_bot" {
  name = "Support bot"
}

# Keep the support bot on OpenAI and Anthropic in the US, preferring
# Anthropic, with zero data retention. Every other control is inherited
# from the organization.
resource "mergegateway_project_controls" "support_bot" {
  project_id = mergegateway_project.support_bot.id

  allowed_regions           = ["US"]
  allowed_vendors           = ["anthropic", "openai"]
  vendor_routing_preference = ["anthropic", "openai"]
  zero_data_retention       = true
}
