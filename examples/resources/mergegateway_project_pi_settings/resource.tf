resource "mergegateway_project" "support_bot" {
  name = "Support bot"
}

# Block prompt injection in the support bot's requests, more strictly than
# the organization does. Every setting left out inherits the organization's value.
resource "mergegateway_project_pi_settings" "support_bot" {
  project_id = mergegateway_project.support_bot.id

  pi_mode               = "block"
  pi_block_threshold    = 0.4
  pi_fail_closed        = true
  pi_allowlist_patterns = ["ignore previous order number"]
}
