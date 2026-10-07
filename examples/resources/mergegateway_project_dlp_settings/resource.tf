resource "mergegateway_project" "support_bot" {
  name = "Support bot"
}

# Entity types are the entity_type values that GET
# /v1/projects/{project_id}/dlp-settings lists.
#
# Redact email addresses and block credit card numbers in this project, and
# stop detecting phone numbers. Every other entity type follows the
# organization's DLP rules.
resource "mergegateway_project_dlp_settings" "support_bot" {
  project_id = mergegateway_project.support_bot.id

  override = {
    EMAIL_ADDRESS = { enabled = true, action = "redact" }
    CREDIT_CARD   = { action = "block" }
    PHONE_NUMBER  = { enabled = false }
  }
}
