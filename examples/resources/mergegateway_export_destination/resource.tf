# Ephemeral variables (Terraform 1.10+) also keep the secrets out of saved plan files.
variable "datadog_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

variable "grafana_cloud_token" {
  type      = string
  sensitive = true
  ephemeral = true
}

# Send request traces to Datadog LLM Observability, without prompts and
# completions (privacy_mode defaults to true), for 1 request in 4.
resource "mergegateway_export_destination" "datadog" {
  name        = "Datadog"
  site        = "datadoghq.com"
  ml_app      = "support-bot"
  sample_rate = 0.25

  # Write-only (Terraform 1.11+): never stored in the plan or state. Terraform
  # sends the secret on create, and again only when the version changes: to
  # rotate it, set the new key and bump the version, such as to 2.
  credential_secret_wo         = var.datadog_api_key
  credential_secret_wo_version = 1
}

# Grafana Cloud authenticates with an instance ID beside the token.
resource "mergegateway_export_destination" "grafana" {
  name                         = "Grafana Cloud"
  otlp_preset                  = "grafana"
  site                         = "prod-us-east-0"
  credential_username          = "123456"
  credential_secret_wo         = var.grafana_cloud_token
  credential_secret_wo_version = 1
  metrics_enabled              = true
}
