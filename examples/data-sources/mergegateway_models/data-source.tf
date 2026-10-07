# Every model that takes images and has at least a 128k context window.
data "mergegateway_models" "vision" {
  input_modality     = "image"
  min_context_length = 128000
}

output "vision_models" {
  value = [for m in data.mergegateway_models.vision.models : m.id if m.status == "available"]
}

# Models that are scheduled to retire, and what to move to.
output "retiring_models" {
  value = {
    for m in data.mergegateway_models.vision.models : m.id => m.replacement_model
    if m.expiration_date != null
  }
}
