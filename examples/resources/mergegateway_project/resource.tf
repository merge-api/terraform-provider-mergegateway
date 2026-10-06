# A project with a monthly budget and the organization's default routing.
resource "mergegateway_project" "support_bot" {
  name        = "Support bot"
  description = "Answers customer support tickets"

  budget_config = {
    amount              = 500
    period              = "monthly"
    enforcement_mode    = "hard_limit"
    alert_thresholds    = [50, 80, 100]
    notification_emails = ["ai-platform@example.com"]
  }
}

# A project with its own routing policy: try gpt-4o first, then gpt-4o-mini.
resource "mergegateway_project" "batch_jobs" {
  name = "Batch jobs"

  routing_policy = {
    strategy = "PRIORITY"
    priority_order = [
      { model = "openai/gpt-4o", priority = 1 },
      { model = "openai/gpt-4o-mini", priority = 2 },
    ]
  }
}
