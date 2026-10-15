# Terraform Provider for Merge Gateway

Use the `mergegateway` provider to manage your [Merge Gateway](https://docs.merge.dev/merge-gateway) setup as code: projects, API keys, routing policies, bring-your-own-key (BYOK) credentials, telemetry export destinations, organization settings, and prompt-injection (PI), data loss prevention (DLP) and blocklist rules. Terraform shows you when someone changes these outside Terraform (drift), and you can import what you already have. The full documentation is on the [Terraform Registry](https://registry.terraform.io/providers/merge-api/mergegateway/latest/docs).

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

## Provider usage

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

Each [resource's page](docs/resources/) has an Import section with the ID it expects.

## Good to know

- `mergegateway_credential` (BYOK) needs a Pro or Enterprise plan.
- `mergegateway_export_destination` needs the telemetry export feature turned on for your organization.
- Secrets in `mergegateway_credential` and `mergegateway_export_destination` are write-only (Terraform 1.11+): Terraform sends them but never saves them in the plan or state. To rotate one, set the new value and bump its `*_wo_version`.
- `mergegateway_organization_settings` only manages the settings you list. The ones you leave out keep their current values.
- On `mergegateway_api_key`, removing `limit` removes the spending limit, and removing `expires_at` makes the key never expire.

## Resources and data sources

Resources:

- [mergegateway_project](docs/resources/project.md): a project, with its budget and routing policy
- [mergegateway_project_controls](docs/resources/project_controls.md): a project's vendor, region and zero data retention overrides
- [mergegateway_project_pi_settings](docs/resources/project_pi_settings.md): a project's prompt-injection protection overrides
- [mergegateway_project_dlp_settings](docs/resources/project_dlp_settings.md): a project's DLP overrides
- [mergegateway_api_key](docs/resources/api_key.md): an API key for sending requests through the Gateway
- [mergegateway_routing_policy](docs/resources/routing_policy.md): an organization routing policy
- [mergegateway_credential](docs/resources/credential.md): a bring-your-own-key vendor credential
- [mergegateway_export_destination](docs/resources/export_destination.md): where the Gateway sends request traces
- [mergegateway_organization_settings](docs/resources/organization_settings.md): the organization's settings
- [mergegateway_organization_pi_settings](docs/resources/organization_pi_settings.md): the organization's prompt-injection protection policy
- [mergegateway_dlp_rule](docs/resources/dlp_rule.md): a custom DLP rule
- [mergegateway_blocklist_rule](docs/resources/blocklist_rule.md): a blocklist or allowlist rule for end customers, providers or models

Data sources:

- [mergegateway_models](docs/data-sources/models.md): the models the organization can route to
- [mergegateway_vendors](docs/data-sources/vendors.md): the active vendors
- [mergegateway_guardrails](docs/data-sources/guardrails.md): the guardrails in force for every project

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0, and >= 1.11 for `mergegateway_credential` and `mergegateway_export_destination`, whose secrets are write-only
- [Go](https://golang.org/doc/install) >= 1.25, to build the provider

## Developing the Provider

Built on HashiCorp's [Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework).

- Provider, resources and data sources: `internal/provider/`
- API client, generated from `openapi.json` by [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen): `internal/client/`
- Examples (`examples/`) and generated documentation (`docs/`, from `templates/` and `examples/`)

To build and install the provider, run `go install`. See [CONTRIBUTING.md](CONTRIBUTING.md) for tests, acceptance tests, regenerating the client and docs, and more.

## License

MIT. Third-party libraries keep their own licenses; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
