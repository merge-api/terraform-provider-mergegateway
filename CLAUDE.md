## Rules
- Source of truth: openapi.json. Never read or depend on the backend code.
- Never edit the generated client by hand; regenerate it with oapi-codegen.
- Every resource follows internal/provider/project_resource.go exactly: structure, naming, error handling, tests.
- Keep it simple: one file per resource or data source, plain terraform-plugin-framework code, no generic resource framework. Add a shared helper only once the same code appears three times.
- Borrow from HashiCorp's scaffolding template and OpenRouter's provider (acceptance test helpers, destroy checks, cleanup of leftover test resources).
- Every resource has acceptance tests for create, no-op re-plan, update, replace, import, destroy and drift.
- Secrets are Sensitive and never logged.
- Do not run terraform apply, terraform destroy or TF_ACC tests. Push, then read CI results with gh.
