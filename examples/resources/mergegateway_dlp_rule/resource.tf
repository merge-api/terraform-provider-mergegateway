# Redact employee IDs such as EMP-123456 from requests.
resource "mergegateway_dlp_rule" "employee_id" {
  entity_type = "EMPLOYEE_ID"
  description = "Internal employee IDs"
  pattern     = "EMP-\\d{6}"
  action      = "redact"
}

# Block requests that mention internal project code names.
resource "mergegateway_dlp_rule" "code_names" {
  entity_type = "PROJECT_CODE_NAME"
  keywords    = ["bluebird", "nightjar"]
  action      = "block"
}
