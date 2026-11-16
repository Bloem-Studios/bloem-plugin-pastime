# Dependencies and licences

> Where the plugin's code comes from, the licence it ships under, and the third-party modules
> it uses.

This page walks through who wrote the plugin, which licence covers it, and which
outside code it includes and under what terms.

## Who wrote it

The plugin was written independently against the published plugin SDK and Pastime's
sync API. No Floppy application source, assets or tests were brought in.

## Which licence covers it

- **The plugin's own code** is licensed under the GNU Affero General Public License
  version 3 ([LICENSE](../LICENSE)), SPDX `AGPL-3.0-only`.
- **Outside modules** keep their own licences. Their licence and notice texts are
  copied word for word into `third_party/licenses/`, and their terms govern those
  parts.
- **Releases published under a proprietary licence** stay under it.

## Words used in the code

| On this page | In the code |
| --- | --- |
| Plugin licence | [LICENSE](../LICENSE); manifest `presentation.license_spdx` `AGPL-3.0-only` |
| Licence and notice texts | `third_party/licenses/<module>@<version>.txt`, plus `.NOTICE` where a module has one |
| Outside modules | the `require` blocks in `go.mod` |

## For developers

The plugin is independently authored against the published SDK and Pastime's sync
API. No Floppy application source, assets or tests were imported. Project code is
AGPL-3.0-only ([LICENSE](../LICENSE), manifest `license_spdx` `AGPL-3.0-only`).
Third-party dependencies retain their own terms, and their notices and any
source-access obligations still apply. The following exact module versions are used
by the executable or its tests. Included licence/NOTICE texts are copied verbatim from
those module versions; their terms govern those materials. HashiCorp MPL dependencies
remain unmodified separate modules.

| Module | Version | Licence | Included text |
| --- | --- | --- | --- |
| `github.com/Bloem-Studios/bloem-plugin-sdk` | `v0.28.0` | Apache-2.0 | [Text](../third_party/licenses/github.com_Bloem-Studios_bloem-plugin-sdk@v0.28.0.txt), [NOTICE](../third_party/licenses/github.com_Bloem-Studios_bloem-plugin-sdk@v0.28.0.txt.NOTICE) |
| `github.com/fatih/color` | `v1.13.0` | MIT | [Text](../third_party/licenses/github.com_fatih_color@v1.13.0.txt) |
| `github.com/golang/protobuf` | `v1.5.4` | BSD-3-Clause | [Text](../third_party/licenses/github.com_golang_protobuf@v1.5.4.txt) |
| `github.com/hashicorp/go-hclog` | `v1.6.3` | MIT | [Text](../third_party/licenses/github.com_hashicorp_go-hclog@v1.6.3.txt) |
| `github.com/hashicorp/go-plugin` | `v1.7.0` | MPL-2.0 | [Text](../third_party/licenses/github.com_hashicorp_go-plugin@v1.7.0.txt) |
| `github.com/hashicorp/yamux` | `v0.1.2` | MPL-2.0 | [Text](../third_party/licenses/github.com_hashicorp_yamux@v0.1.2.txt) |
| `github.com/mattn/go-colorable` | `v0.1.12` | MIT | [Text](../third_party/licenses/github.com_mattn_go-colorable@v0.1.12.txt) |
| `github.com/mattn/go-isatty` | `v0.0.17` | MIT | [Text](../third_party/licenses/github.com_mattn_go-isatty@v0.0.17.txt) |
| `github.com/oklog/run` | `v1.1.0` | Apache-2.0 | [Text](../third_party/licenses/github.com_oklog_run@v1.1.0.txt) |
| `golang.org/x/net` | `v0.58.0` | BSD-3-Clause | [Text](../third_party/licenses/golang.org_x_net@v0.58.0.txt) |
| `golang.org/x/sys` | `v0.47.0` | BSD-3-Clause | [Text](../third_party/licenses/golang.org_x_sys@v0.47.0.txt) |
| `golang.org/x/text` | `v0.41.0` | BSD-3-Clause | [Text](../third_party/licenses/golang.org_x_text@v0.41.0.txt) |
| `google.golang.org/genproto/googleapis/rpc` | `v0.0.0-20260526163538-3dc84a4a5aaa` | Apache-2.0 | [Text](../third_party/licenses/google.golang.org_genproto_googleapis_rpc@v0.0.0-20260526163538-3dc84a4a5aaa.txt) |
| `google.golang.org/grpc` | `v1.83.2` | Apache-2.0 | [Text](../third_party/licenses/google.golang.org_grpc@v1.83.2.txt) |
| `google.golang.org/protobuf` | `v1.36.11` | BSD-3-Clause | [Text](../third_party/licenses/google.golang.org_protobuf@v1.36.11.txt) |

The Go standard library/toolchain is covered by its upstream BSD licence and toolchain
notices. This inventory describes compiled packages and test dependencies, not every
unused transitive module in the module graph.

Related pages:

- [README](../README.md#licence).
- [Bloem plugin catalog](https://github.com/Bloem-Studios/bloem-plugins), which carries
  the release binaries.
