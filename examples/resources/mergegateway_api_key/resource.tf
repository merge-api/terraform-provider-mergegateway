resource "mergegateway_project" "support_bot" {
  name = "Support bot"
}

# A key scoped to one project, capped at $100 a month, limited to two models.
resource "mergegateway_api_key" "support_bot" {
  name           = "Support bot production"
  project_id     = mergegateway_project.support_bot.id
  limit          = 100
  limit_reset    = "monthly"
  allowed_models = ["openai/gpt-4o", "openai/gpt-4o-mini"]
}

# An organization-wide key with no cap, which can pick a project per request
# with the X-Project-Id header.
resource "mergegateway_api_key" "internal_tools" {
  name = "Internal tools"
}

# The raw key is only known after create. Pass it on without printing it, for
# example to a secret store, or read it with `terraform output -raw`.
output "support_bot_api_key" {
  value     = mergegateway_api_key.support_bot.key
  sensitive = true
}
