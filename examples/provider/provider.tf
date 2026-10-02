provider "mergegateway" {
  # Or set the MERGE_GATEWAY_MANAGEMENT_KEY environment variable.
  api_key = var.merge_gateway_management_key

  # Optional. Defaults to https://api-gateway.merge.dev.
  # base_url = "https://api-gateway.merge.dev"
}

variable "merge_gateway_management_key" {
  type      = string
  sensitive = true
}
