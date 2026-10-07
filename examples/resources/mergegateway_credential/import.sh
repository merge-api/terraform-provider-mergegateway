# Import a credential by its ID. The API never returns the secrets, so after an
# import set the one the vendor uses (api_key_wo, aws_secret_access_key_wo or
# service_account_json_wo) and its _wo_version in the configuration: the first
# apply sends it again.
terraform import mergegateway_credential.azure 2f9c3a7e-5b1d-4c8e-9f60-1a2b3c4d5e6f
