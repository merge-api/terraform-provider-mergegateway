## 0.1.0 (Unreleased)

First release of the Merge Gateway provider.

FEATURES:

* **New Resource:** `mergegateway_project`
* **New Resource:** `mergegateway_project_controls`
* **New Resource:** `mergegateway_project_pi_settings`
* **New Resource:** `mergegateway_project_dlp_settings`
* **New Resource:** `mergegateway_api_key`
* **New Resource:** `mergegateway_routing_policy`
* **New Resource:** `mergegateway_credential`
* **New Resource:** `mergegateway_export_destination`
* **New Resource:** `mergegateway_organization_settings`
* **New Resource:** `mergegateway_organization_pi_settings`
* **New Resource:** `mergegateway_dlp_rule`
* **New Resource:** `mergegateway_blocklist_rule`
* **New Data Source:** `mergegateway_models`
* **New Data Source:** `mergegateway_vendors`
* **New Data Source:** `mergegateway_guardrails`

NOTES:

* Every resource can be imported by ID.
* Credential API keys and export destination secrets are write-only and never stored in state. They need Terraform 1.11 or later.
