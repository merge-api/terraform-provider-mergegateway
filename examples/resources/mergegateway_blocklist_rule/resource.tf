# Keep every request away from one provider.
resource "mergegateway_blocklist_rule" "no_deepseek" {
  providers = ["deepseek"]
  reason    = "Not approved by security review"
}

# Let two end customers use only OpenAI's GPT-4o models.
resource "mergegateway_blocklist_rule" "acme_pin" {
  rule_type    = "pin"
  customer_ids = ["acme-prod", "acme-staging"]
  models       = ["openai/gpt-4o", "openai/gpt-4o-mini"]
  reason       = "Contractual model restriction"
}
