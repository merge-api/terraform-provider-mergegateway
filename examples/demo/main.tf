# A small demo: one Merge Gateway project with a budget. See README.md.

terraform {
  required_providers {
    mergegateway = {
      source = "merge-api/mergegateway"
    }
  }
}

variable "base_url" {
  description = "Merge Gateway API URL, such as http://localhost:8001 or https://api-gateway-develop.merge.dev."
  type        = string
}

variable "api_key" {
  description = "A Merge Gateway management key with the manage_projects scope."
  type        = string
  sensitive   = true
}

provider "mergegateway" {
  base_url = var.base_url
  api_key  = var.api_key
}

resource "mergegateway_project" "demo" {
  name        = "Terraform demo"
  description = "Created by the mergegateway Terraform provider demo"

  budget_config = {
    amount           = 100
    period           = "monthly"
    enforcement_mode = "soft_limit"
    alert_thresholds = [50, 80, 100]
  }
}

output "project_id" {
  value = mergegateway_project.demo.id
}

output "project_slug" {
  value = mergegateway_project.demo.slug
}
