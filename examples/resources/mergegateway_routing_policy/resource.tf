# A PRIORITY policy: try gpt-4o first, then gpt-4o-mini. Requests route with
# it by sending "@alias/fast-fallback" as the model.
resource "mergegateway_routing_policy" "fast_fallback" {
  name        = "Fast fallback"
  description = "gpt-4o with gpt-4o-mini as a fallback"
  slug        = "fast-fallback"
  strategy    = "PRIORITY"

  priority_order = [
    { model = "openai/gpt-4o", priority = 1 },
    { model = "openai/gpt-4o-mini", priority = 2 },
  ]
}

# An INTELLIGENT policy that picks the cheapest suitable model.
resource "mergegateway_routing_policy" "cheapest" {
  name           = "Cheapest"
  strategy       = "INTELLIGENT"
  axis           = "COST"
  allowed_models = ["openai/gpt-4o", "openai/gpt-4o-mini"]
}
