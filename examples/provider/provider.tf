# Reads the management key from the MERGE_GATEWAY_MANAGEMENT_KEY environment
# variable. To set it in the config instead, use `api_key`.
provider "mergegateway" {}

# A project for a support bot.
resource "mergegateway_project" "support_bot" {
  name        = "Support bot"
  description = "Answers customer support tickets"
}

# An API key for the support bot's project that can spend at most $100 a month.
resource "mergegateway_api_key" "support_bot" {
  name        = "Support bot production"
  project_id  = mergegateway_project.support_bot.id
  limit       = 100
  limit_reset = "monthly"
}

# A routing policy: try gpt-4o first, then fall back to gpt-4o-mini. Requests
# use it by sending "@alias/fast-fallback" as the model.
resource "mergegateway_routing_policy" "fast_fallback" {
  name     = "Fast fallback"
  slug     = "fast-fallback"
  strategy = "PRIORITY"

  priority_order = [
    { model = "openai/gpt-4o", priority = 1 },
    { model = "openai/gpt-4o-mini", priority = 2 },
  ]
}

# The raw key is only known after create. Read it with
# `terraform output -raw support_bot_api_key`.
output "support_bot_api_key" {
  value     = mergegateway_api_key.support_bot.key
  sensitive = true
}
