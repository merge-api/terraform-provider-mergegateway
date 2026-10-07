data "mergegateway_vendors" "all" {}

# The vendors you can add your own credential for with mergegateway_credential.
output "byok_vendors" {
  value = [for v in data.mergegateway_vendors.all.vendors : v.slug if v.allow_byok]
}
