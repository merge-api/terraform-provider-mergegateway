---
page_title: "mergegateway Provider"
description: |-
  Manage Merge Gateway configuration as code.
---

# mergegateway Provider

Use the `mergegateway` provider to manage your [Merge Gateway](https://docs.merge.dev/merge-gateway) setup as code: projects, API keys, routing policies, bring-your-own-key (BYOK) credentials, telemetry export destinations, organization settings, and prompt-injection (PI), data loss prevention (DLP) and blocklist rules. Terraform shows you when someone changes these outside Terraform (drift), and you can import what you already have.

## Installation

Add the provider to your configuration:

```terraform
terraform {
  required_providers {
    mergegateway = {
      source  = "merge-api/mergegateway"
      version = "~> 0.1"
    }
  }
}
```

Then run:

```shell
terraform init
```

## Example Usage

The provider signs in with a **management key**. To create one, open the Merge Gateway dashboard and go to **API keys** -> **Management keys**.

- A management key gets the permissions of the person who creates it, so have an organization admin create it.
- A management key manages settings only: it can't call models. Your apps use the API keys you create, such as with `mergegateway_api_key`.

This example creates a project, an API key for that project with a monthly spending limit, and a routing policy:

```terraform
# Reads the management key from the MERGE_GATEWAY_MANAGEMENT_KEY environment
# variable. To set it in the config instead, use `api_key`.
provider "mergegateway" {}

# A project for a support bot.
resource "mergegateway_project" "support_bot" {
  name        = "Support bot"
  description = "Answers customer support tickets"
}

# An API key for the support bot's project that can spend at most $100 a month.
resource "mergegateway_api_key" "support_bot" {
  name        = "Support bot production"
  project_id  = mergegateway_project.support_bot.id
  limit       = 100
  limit_reset = "monthly"
}

# A routing policy: try gpt-4o first, then fall back to gpt-4o-mini. Requests
# use it by sending "@alias/fast-fallback" as the model.
resource "mergegateway_routing_policy" "fast_fallback" {
  name     = "Fast fallback"
  slug     = "fast-fallback"
  strategy = "PRIORITY"

  priority_order = [
    { model = "openai/gpt-4o", priority = 1 },
    { model = "openai/gpt-4o-mini", priority = 2 },
  ]
}

# The raw key is only known after create. Read it with
# `terraform output -raw support_bot_api_key`.
output "support_bot_api_key" {
  value     = mergegateway_api_key.support_bot.key
  sensitive = true
}
```

## Authentication

| Setting | Provider argument | Environment variable | Default |
|---|---|---|---|
| Management key | `api_key` | `MERGE_GATEWAY_MANAGEMENT_KEY` | none (required) |
| API URL | `base_url` | none | `https://api-gateway.merge.dev` |

If both `api_key` and `MERGE_GATEWAY_MANAGEMENT_KEY` are set, `api_key` wins. Keep the key out of your files: set the environment variable, or pass `api_key` from a sensitive variable.

## Importing existing setup

You can bring things you already set up in the dashboard under Terraform. Write the resource in your config, then import it by its ID, either with an `import` block (Terraform 1.5+):

```terraform
import {
  to = mergegateway_project.support_bot
  id = "6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11"
}
```

or with the `terraform import` command:

```shell
terraform import mergegateway_project.support_bot 6f1c1f5e-8a8b-4c43-9a0e-3f0f6d0c8b11
```

Each resource's page has an Import section with the ID it expects.

## Good to know

- `mergegateway_credential` (BYOK) needs a Pro or Enterprise plan.
- `mergegateway_export_destination` needs the telemetry export feature turned on for your organization.
- Secrets in `mergegateway_credential` and `mergegateway_export_destination` are write-only (Terraform 1.11+): Terraform sends them but never saves them in the plan or state. To rotate one, set the new value and bump its `*_wo_version`.
- `mergegateway_organization_settings` only manages the settings you list. The ones you leave out keep their current values.
- On `mergegateway_api_key`, removing `limit` removes the spending limit, and removing `expires_at` makes the key never expire.

## Resources and data sources

Resources:

- [mergegateway_project](resources/project): a project, with its budget and routing policy
- [mergegateway_project_controls](resources/project_controls): a project's vendor, region and zero data retention overrides
- [mergegateway_project_pi_settings](resources/project_pi_settings): a project's prompt-injection protection overrides
- [mergegateway_project_dlp_settings](resources/project_dlp_settings): a project's DLP overrides
- [mergegateway_api_key](resources/api_key): an API key for sending requests through the Gateway
- [mergegateway_routing_policy](resources/routing_policy): an organization routing policy
- [mergegateway_credential](resources/credential): a bring-your-own-key vendor credential
- [mergegateway_export_destination](resources/export_destination): where the Gateway sends request traces
- [mergegateway_organization_settings](resources/organization_settings): the organization's settings
- [mergegateway_organization_pi_settings](resources/organization_pi_settings): the organization's prompt-injection protection policy
- [mergegateway_dlp_rule](resources/dlp_rule): a custom DLP rule
- [mergegateway_blocklist_rule](resources/blocklist_rule): a blocklist or allowlist rule for end customers, providers or models

Data sources:

- [mergegateway_models](data-sources/models): the models the organization can route to
- [mergegateway_vendors](data-sources/vendors): the active vendors
- [mergegateway_guardrails](data-sources/guardrails): the guardrails in force for every project

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `api_key` (String, Sensitive) Merge Gateway management key. Can also be set with the `MERGE_GATEWAY_MANAGEMENT_KEY` environment variable.
- `base_url` (String) Base URL of the Merge Gateway API. Defaults to `https://api-gateway.merge.dev`.
