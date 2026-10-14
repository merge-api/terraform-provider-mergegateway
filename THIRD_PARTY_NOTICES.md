# Third-party notices

The provider binary includes the Go modules below. Each keeps its own license.
The MPL-2.0 modules are used unmodified, and their source is available at the
links below.

The list covers every module compiled into the binary (`go list -deps .`), not
modules used only by tests or tools. Their full license, notice and patent
texts are in [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES), which every release
archive includes.

| Module | Version | License | Source |
| --- | --- | --- | --- |
| github.com/apapsch/go-jsonmerge/v2 | v2.0.0 | MIT | https://github.com/apapsch/go-jsonmerge/tree/v2.0.0 |
| github.com/fatih/color | v1.18.0 | MIT | https://github.com/fatih/color/tree/v1.18.0 |
| github.com/golang/protobuf | v1.5.4 | BSD-3-Clause | https://github.com/golang/protobuf/tree/v1.5.4 |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | https://github.com/google/uuid/tree/v1.6.0 |
| github.com/hashicorp/go-hclog | v1.6.3 | MIT | https://github.com/hashicorp/go-hclog/tree/v1.6.3 |
| github.com/hashicorp/go-plugin | v1.7.0 | MPL-2.0 | https://github.com/hashicorp/go-plugin/tree/v1.7.0 |
| github.com/hashicorp/go-uuid | v1.0.3 | MPL-2.0 | https://github.com/hashicorp/go-uuid/tree/v1.0.3 |
| github.com/hashicorp/terraform-plugin-framework | v1.19.0 | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-framework/tree/v1.19.0 |
| github.com/hashicorp/terraform-plugin-framework-validators | v0.19.0 | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-framework-validators/tree/v0.19.0 |
| github.com/hashicorp/terraform-plugin-go | v0.31.0 | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-go/tree/v0.31.0 |
| github.com/hashicorp/terraform-plugin-log | v0.11.0 | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-log/tree/v0.11.0 |
| github.com/hashicorp/terraform-registry-address | v0.4.0 | MPL-2.0 | https://github.com/hashicorp/terraform-registry-address/tree/v0.4.0 |
| github.com/hashicorp/terraform-svchost | v0.2.1 | MPL-2.0 | https://github.com/hashicorp/terraform-svchost/tree/v0.2.1 |
| github.com/hashicorp/yamux | v0.1.2 | MPL-2.0 | https://github.com/hashicorp/yamux/tree/v0.1.2 |
| github.com/mattn/go-colorable | v0.1.14 | MIT | https://github.com/mattn/go-colorable/tree/v0.1.14 |
| github.com/mattn/go-isatty | v0.0.20 | MIT | https://github.com/mattn/go-isatty/tree/v0.0.20 |
| github.com/mitchellh/go-testing-interface | v1.14.1 | MIT | https://github.com/mitchellh/go-testing-interface/tree/v1.14.1 |
| github.com/oapi-codegen/nullable | v1.2.0 | Apache-2.0 | https://github.com/oapi-codegen/nullable/tree/v1.2.0 |
| github.com/oapi-codegen/runtime | v1.7.0 | Apache-2.0 | https://github.com/oapi-codegen/runtime/tree/v1.7.0 |
| github.com/oklog/run | v1.2.0 | Apache-2.0 | https://github.com/oklog/run/tree/v1.2.0 |
| github.com/vmihailenco/msgpack/v5 | v5.4.1 | BSD-2-Clause | https://github.com/vmihailenco/msgpack/tree/v5.4.1 |
| github.com/vmihailenco/tagparser/v2 | v2.0.0 | BSD-2-Clause | https://github.com/vmihailenco/tagparser/tree/v2.0.0 |
| golang.org/x/net | v0.55.0 | BSD-3-Clause | https://github.com/golang/net/tree/v0.55.0 |
| golang.org/x/sys | v0.45.0 | BSD-3-Clause | https://github.com/golang/sys/tree/v0.45.0 |
| golang.org/x/text | v0.37.0 | BSD-3-Clause | https://github.com/golang/text/tree/v0.37.0 |
| google.golang.org/genproto/googleapis/rpc | v0.0.0-20260414002931-afd174a4e478 | Apache-2.0 | https://github.com/googleapis/go-genproto/tree/afd174a4e478/googleapis/rpc |
| google.golang.org/grpc | v1.82.1 | Apache-2.0 | https://github.com/grpc/grpc-go/tree/v1.82.1 |
| google.golang.org/protobuf | v1.36.11 | BSD-3-Clause | https://github.com/protocolbuffers/protobuf-go/tree/v1.36.11 |

The Go standard library is also compiled in (BSD-3-Clause, https://go.dev/LICENSE).

`make generate` regenerates THIRD_PARTY_LICENSES with
`scripts/third-party-licenses.sh`, which fails if a module in the binary has no
row in the table above. When it does, add the row by hand.
