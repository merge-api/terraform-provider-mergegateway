data "mergegateway_guardrails" "all" {}

# Projects where prompt-injection protection doesn't block.
output "projects_without_pi_blocking" {
  value = [
    for g in data.mergegateway_guardrails.all.guardrails : g.project_name
    if g.pi_settings.pi_mode != "block"
  ]
}
