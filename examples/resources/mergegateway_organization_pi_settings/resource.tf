# Block likely prompt injections in every project that doesn't override the
# policy, and redact flagged responses. Settings left out take their defaults.
resource "mergegateway_organization_pi_settings" "this" {
  pi_mode               = "block"
  pi_block_threshold    = 0.6
  pi_indirect_mode      = "alert"
  pi_output_action      = "redact"
  pi_allowlist_patterns = ["ignore previous formatting"]
}
