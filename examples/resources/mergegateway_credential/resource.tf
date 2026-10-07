# Ephemeral variables (Terraform 1.10+) also keep the secrets out of saved plan files.
variable "azure_openai_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

variable "vertex_service_account_json" {
  type      = string
  sensitive = true
  ephemeral = true
}

# The organization's own Azure OpenAI key, used instead of Merge-managed access.
resource "mergegateway_credential" "azure" {
  vendor = "azure"
  name   = "Azure OpenAI production"

  # Write-only (Terraform 1.11+): never stored in the plan or state. Terraform
  # sends the key on create, and again only when the version changes: to rotate
  # it, set the new key and bump the version, such as to 2.
  api_key_wo         = var.azure_openai_api_key
  api_key_wo_version = 1

  api_base = "https://example.openai.azure.com"
  config = {
    api_version      = "2024-02-15-preview"
    azure_deployment = "gpt-4o"
  }
}

# Vertex AI authenticates with a service account instead of an API key.
resource "mergegateway_credential" "vertex" {
  vendor                          = "vertexai"
  fallback_to_managed             = false
  service_account_json_wo         = var.vertex_service_account_json
  service_account_json_wo_version = 1
  config = {
    vertex_project  = "my-gcp-project"
    vertex_location = "us-central1"
  }
}
