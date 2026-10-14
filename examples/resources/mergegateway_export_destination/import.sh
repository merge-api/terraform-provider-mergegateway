# Import an export destination by its ID. The API never returns the secret, so
# after an import set credential_secret_wo and credential_secret_wo_version in
# the configuration: the first apply sends the secret again.
terraform import mergegateway_export_destination.datadog 7c1d2e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f
