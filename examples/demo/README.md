# Demo: a Merge Gateway project with Terraform

`main.tf` creates one project, "Terraform demo", with a $100 monthly budget. You run it with a provider built from this repo, against either the local Gateway stack or the dev Gateway.

You need Go and Terraform.

## 1. Build the provider and point Terraform at it

The provider isn't on the Terraform Registry yet, so Terraform loads the binary you build.

From the root of this repo:

```shell
go install .
go env GOPATH   # the provider is now in <GOPATH>/bin/terraform-provider-mergegateway
```

Create `~/.terraformrc` (or add to it), with the absolute path of `<GOPATH>/bin`:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/merge-api/mergegateway" = "/Users/<you>/go/bin"
  }
  direct {}
}
```

Run `go install .` again after changing the provider code. Remove the `dev_overrides` block when you're done; it applies to every Terraform run on your machine.

## 2. Get a management key

The demo needs a management key with the `manage_projects` scope. Pick one of the two environments.

### Local stack

Start the Gateway stack in Docker, as in the main [README](../../README.md#acceptance-tests):

```shell
# In merge-gateway, with merge-gateway-control-backend and merge-gateway-control-frontend checked out next to it:
docker compose -f docker-compose.local.yml up -d --build --wait control-plane data-plane-grpc control-plane-frontend
```

Then, from the root of this repo, create a test org and key:

```shell
export TF_VAR_base_url=http://localhost:8001
export TF_VAR_api_key="$(scripts/seed-acceptance.sh)"
```

The script prints the account it registered, such as `Registering tf-acc-1a2b3c4d5e6f@example.com`. Its password is `TfAcc-1a2b3c4d5e6f-Aa1!` (the same hex ID). You'll use them to see the project in the dashboard.

### Dev

1. Log in to the dev dashboard, https://gateway-develop.merge.dev, with an org you can experiment in.
2. Go to **API keys**, then **Management keys**, and create a key with the `manage_projects` scope. Copy it.
3. Set the variables:

```shell
export TF_VAR_base_url=https://api-gateway-develop.merge.dev
export TF_VAR_api_key='<the management key>'
```

## 3. Plan, apply, look, destroy

```shell
cd examples/demo
terraform plan
terraform apply
```

Skip `terraform init`: with `dev_overrides`, Terraform uses your build directly, and `init` fails because it can't find the provider on the registry. Terraform prints a warning about the overrides on every command; that's expected.

`terraform plan` shows one project to create. `terraform apply` asks for confirmation, creates it, and prints `project_id` and `project_slug`. Run `terraform plan` again: it should say "No changes".

**See it in the dashboard.** Open the dashboard and log in (local: http://localhost:8080 with the account the seed script printed; dev: https://gateway-develop.merge.dev with the org the key belongs to). Go to **Projects**: "Terraform demo" is in the list, with its $100 monthly budget.

**Try a change.** Edit `main.tf` (for example, set `amount = 200` or rename the project), then run `terraform apply` again. Terraform updates the project in place, and the dashboard shows the change after a refresh. If you change the project in the dashboard instead, `terraform plan` shows the difference and `terraform apply` puts it back.

**Clean up:**

```shell
terraform destroy
```

The project disappears from the dashboard. Local runs leave `terraform.tfstate` in `examples/demo`; it's ignored by git and safe to delete after destroy.

## Troubleshooting

- `HTTP 401 Unauthorized: Invalid management API key`: `TF_VAR_api_key` is wrong, or belongs to the other environment.
- `HTTP 403 Forbidden`: the key lacks the `manage_projects` scope. Create one that has it.
- `Failed to query available provider packages` or `Inconsistent dependency lock file`: you ran `terraform init`, or the `dev_overrides` path is wrong. Check the path, delete `.terraform` and `.terraform.lock.hcl` in this directory, and run `terraform plan` without `init`.
