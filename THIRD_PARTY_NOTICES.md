# Third-party notices

The provider binary includes the Go modules below. Each keeps its own license.
The MPL-2.0 modules are used unmodified, and their source is available at the
links below.

The list covers every module compiled into the binary (`go list -deps .`), not
modules used only by tests or tools. Their full license, notice and patent
texts are in [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES), which every release
archive includes.

| Module | License | Source |
| --- | --- | --- |
| github.com/apapsch/go-jsonmerge/v2 | MIT | https://github.com/apapsch/go-jsonmerge |
| github.com/fatih/color | MIT | https://github.com/fatih/color |
| github.com/golang/protobuf | BSD-3-Clause | https://github.com/golang/protobuf |
| github.com/google/uuid | BSD-3-Clause | https://github.com/google/uuid |
| github.com/hashicorp/go-hclog | MIT | https://github.com/hashicorp/go-hclog |
| github.com/hashicorp/go-plugin | MPL-2.0 | https://github.com/hashicorp/go-plugin |
| github.com/hashicorp/go-uuid | MPL-2.0 | https://github.com/hashicorp/go-uuid |
| github.com/hashicorp/terraform-plugin-framework | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-framework |
| github.com/hashicorp/terraform-plugin-framework-validators | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-framework-validators |
| github.com/hashicorp/terraform-plugin-go | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-go |
| github.com/hashicorp/terraform-plugin-log | MPL-2.0 | https://github.com/hashicorp/terraform-plugin-log |
| github.com/hashicorp/terraform-registry-address | MPL-2.0 | https://github.com/hashicorp/terraform-registry-address |
| github.com/hashicorp/terraform-svchost | MPL-2.0 | https://github.com/hashicorp/terraform-svchost |
| github.com/hashicorp/yamux | MPL-2.0 | https://github.com/hashicorp/yamux |
| github.com/mattn/go-colorable | MIT | https://github.com/mattn/go-colorable |
| github.com/mattn/go-isatty | MIT | https://github.com/mattn/go-isatty |
| github.com/mitchellh/go-testing-interface | MIT | https://github.com/mitchellh/go-testing-interface |
| github.com/oapi-codegen/nullable | Apache-2.0 | https://github.com/oapi-codegen/nullable |
| github.com/oapi-codegen/runtime | Apache-2.0 | https://github.com/oapi-codegen/runtime |
| github.com/oklog/run | Apache-2.0 | https://github.com/oklog/run |
| github.com/vmihailenco/msgpack/v5 | BSD-2-Clause | https://github.com/vmihailenco/msgpack |
| github.com/vmihailenco/tagparser/v2 | BSD-2-Clause | https://github.com/vmihailenco/tagparser |
| golang.org/x/net | BSD-3-Clause | https://github.com/golang/net |
| golang.org/x/sys | BSD-3-Clause | https://github.com/golang/sys |
| golang.org/x/text | BSD-3-Clause | https://github.com/golang/text |
| google.golang.org/genproto/googleapis/rpc | Apache-2.0 | https://github.com/googleapis/go-genproto/tree/main/googleapis/rpc |
| google.golang.org/grpc | Apache-2.0 | https://github.com/grpc/grpc-go |
| google.golang.org/protobuf | BSD-3-Clause | https://github.com/protocolbuffers/protobuf-go |

The Go standard library is also compiled in (BSD-3-Clause, https://go.dev/LICENSE).

`make generate` regenerates THIRD_PARTY_LICENSES with
`scripts/third-party-licenses.sh`, which fails if a module in the binary has no
row in the table above. When it does, add the row by hand. Rows don't name a
version, so version updates don't need a change here.
