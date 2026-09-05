# Refactor acceptance and maintenance contract

Spec [#31](https://github.com/zhong/droply/issues/31), final verification ticket
[#60](https://github.com/zhong/droply/issues/60). The original review baseline is
`473ef90` (M0–M3). All 28 prerequisite tickets are merged. Final local acceptance
runs on source commit `61c77624a2f041d9b3e945f43b9105805a8ceca6`, based on main
`323e9a2` with the remaining cookie test export moved into `export_test.go`.
Subsequent changes in this PR are documentation only.

## Delivered changes

| Issue | Scope | Merged PR |
| --- | --- | --- |
| [#32](https://github.com/zhong/droply/issues/32) | PR 持续验收与兼容基线 | [#63](https://github.com/zhong/droply/pull/63) |
| [#33](https://github.com/zhong/droply/issues/33) | CLI 配置失败可见且保存不破坏原文件 | [#64](https://github.com/zhong/droply/pull/64) |
| [#34](https://github.com/zhong/droply/issues/34) | 企业微信请求可取消并有执行期限 | [#65](https://github.com/zhong/droply/pull/65) |
| [#35](https://github.com/zhong/droply/issues/35) | OAuth 网络往返移出产物读取锁 | [#69](https://github.com/zhong/droply/pull/69) |
| [#36](https://github.com/zhong/droply/issues/36) | 纠正 Cloudflare 模式挑战方式说明 | [#61](https://github.com/zhong/droply/pull/61) |
| [#37](https://github.com/zhong/droply/issues/37) | 对齐 Go 最低版本与发行工具链 | [#72](https://github.com/zhong/droply/pull/72) |
| [#38](https://github.com/zhong/droply/issues/38) | 应用有明确收益的现代 Go 表达 | [#87](https://github.com/zhong/droply/pull/87) |
| [#39](https://github.com/zhong/droply/issues/39) | 恢复可读的控制台维护源码 | [#62](https://github.com/zhong/droply/pull/62) |
| [#40](https://github.com/zhong/droply/issues/40) | 控制台使用具名数据与渲染步骤 | [#66](https://github.com/zhong/droply/pull/66) |
| [#41](https://github.com/zhong/droply/issues/41) | 统一 CLI 命令输出与 HTTP 响应处理 | [#68](https://github.com/zhong/droply/pull/68) |
| [#42](https://github.com/zhong/droply/issues/42) | 安装脚本建立单一维护源 | [#67](https://github.com/zhong/droply/pull/67) |
| [#43](https://github.com/zhong/droply/issues/43) | 显式声明管理操作并集中项目授权 | [#82](https://github.com/zhong/droply/pull/82) |
| [#44](https://github.com/zhong/droply/issues/44) | 审计由操作显式提供目标和语义结果 | [#86](https://github.com/zhong/droply/pull/86) |
| [#45](https://github.com/zhong/droply/issues/45) | 默认 HTTP handler 使用安全统一入口 | [#81](https://github.com/zhong/droply/pull/81) |
| [#46](https://github.com/zhong/droply/issues/46) | 一次解析站点身份并显式传递私有状态 | [#85](https://github.com/zhong/droply/pull/85) |
| [#47](https://github.com/zhong/droply/issues/47) | 复用登录限流实现并保留独立额度 | [#73](https://github.com/zhong/droply/pull/73) |
| [#48](https://github.com/zhong/droply/issues/48) | 为访客限流增加明确容量上限 | [#76](https://github.com/zhong/droply/pull/76) |
| [#49](https://github.com/zhong/droply/issues/49) | 整理 SQLite 访问规则边界与扫描约定 | [#77](https://github.com/zhong/droply/pull/77) |
| [#50](https://github.com/zhong/droply/issues/50) | 复用生产指针切换的事务步骤 | [#74](https://github.com/zhong/droply/pull/74) |
| [#51](https://github.com/zhong/droply/issues/51) | 将 fixture 状态转换移出生产接口 | [#80](https://github.com/zhong/droply/pull/80) |
| [#52](https://github.com/zhong/droply/issues/52) | 提取具体启动配置与资源装配步骤 | [#71](https://github.com/zhong/droply/pull/71) |
| [#53](https://github.com/zhong/droply/issues/53) | 无效启动配置在持久化变更前失败 | [#79](https://github.com/zhong/droply/pull/79) |
| [#54](https://github.com/zhong/droply/issues/54) | 有序迁移与固定历史 schema 验收 | [#83](https://github.com/zhong/droply/pull/83) |
| [#55](https://github.com/zhong/droply/issues/55) | 统一签发与握手等待预算 | [#75](https://github.com/zhong/droply/pull/75) |
| [#56](https://github.com/zhong/droply/issues/56) | 安全解包与部署发布生命周期解耦 | [#78](https://github.com/zhong/droply/pull/78) |
| [#57](https://github.com/zhong/droply/issues/57) | 评估并按证据减少恢复中间产物 | [#84](https://github.com/zhong/droply/pull/84) |
| [#58](https://github.com/zhong/droply/issues/58) | 评估不可变静态规则的编译缓存 | [#88](https://github.com/zhong/droply/pull/88) |
| [#59](https://github.com/zhong/droply/issues/59) | 独立验证终结 rewrite 的循环边界 | [#70](https://github.com/zhong/droply/pull/70) |

Structural changes, behavior fixes, toolchain alignment and performance work were
landed separately. PR #87 retains its seven theme commits; each independently
passed build, integration vet and integration tests. Other PRs are independent
squash commits. Reverting a change still requires checking later dependencies;
independent commits do not imply every arbitrary reverse ordering is safe.

## Maintenance boundaries

| Concern | Current owner and invariant |
| --- | --- |
| Management authorization | `internal/server/operations.go` declares operation policy; routes select an operation before authentication. Project identity/roles are handled in `project_authorization.go`. Publication rechecks identity, membership and token permissions after lock waits. |
| Audit | Handlers report finite results and numeric targets; `audit.go` reserves pending before mutation and finalizes status. It does not capture or decode response JSON. Lost acknowledgement or finalization remains pending. |
| HTTP and site policy | Default `Server.ServeHTTP` routes by Host; private `serveAPI` is internal. `siteHost`/`siteRequest` carry request state, with live authorization and production selection. Artifact read protection lasts through the response. |
| Static configuration | Only immutable artifact configuration is cached, at most 64 entries. Cleanup invalidates it under the deployment lock. Request identity, access, aliases, private/preview policy and production pointers are never cached. |
| CLI | Configuration reads return errors; atomic replacement protects old files. Commands return errors and use command output/context. Shared response handling retains upload no-replay semantics. |
| OAuth and limiting | External requests receive caller cancellation and a ten-second limit per HTTP request. OAuth exchanges run outside the deployment lock, then revalidate live state. Account and visitor limiters have separate instances and quotas. |
| SQLite | One concrete implementation, domain files and local scanners. An explicit ordered migration list uses fixed historical schemas, snapshots and reentrant upgrades. Production switching stays inside its transaction; fixture-only publication APIs are gone. |
| Startup and files | Validate configuration/material before opening managed data; retain locks, snapshots and shutdown order. `safetree` owns bounded safe unpacking; backup and artifact publication own their lifecycles. |
| Toolchain and frontend | Go 1.27.1 minimum and exact CI/release compiler. Native embedded JS/CSS remain readable; no frontend framework. `scripts/` owns installers and `make website` updates standalone downloads. |

No ORM, DI container, generic DAO, command DSL, event bus, JSON-library migration
or new service dependency was introduced. The remaining `Store` interface is
kept until an actual independent consumer needs a narrower contract. The public
CLI's legacy config fields, `--caddy-admin` (ignored) and `--site-addr` compatibility
remain intentional; they are not unused migration scaffolding. No Caddy module
or running Caddy process is required. Historical `docs/superpowers` documents
remain historical records, not current deployment instructions.

## Observable adjustments

- Corrupt/unreadable CLI configuration and unknown contexts fail visibly instead
  of silently selecting the default API. Failed writes preserve old content.
- WeCom HTTP operations terminate on cancellation or their finite deadline; a
  lookup can make two sequential requests. Incomplete startup configuration now
  fails before database/artifact mutation instead of disabling only the feature.
- Visitor limiter capacity is 4096. At capacity, new IPs are refused rather than
  discarding another visitor's quota; existing entries keep their budget.
- The natural Go HTTP handler now performs safe Host routing. Public HTTP paths,
  CLI commands, URL formats and successful response fields are retained.
- Audit records distinguish business success/failure from delivery failures and
  unknown commit results. A pending record is not evidence of success.
- Building from source requires Go 1.27.1. Prebuilt binaries still require no Go.

Backup format remains version 1, SQLite `user_version` remains 3, and existing
API credentials, cookie verification, domain verification, rollback, HMAC,
certificate and external-file restore contracts remain covered. See the
[compatibility baseline](compatibility-baseline.md) for the public contract.

## Conditional work

[#57](restore-performance.md) removes a second restore content copy only after
archive, manifest, SQLite and reference validation. The measurement records both
latency/allocation and logical staging footprint; it does not claim RSS or
physical block savings. Native Windows staging has a separate executed CI test;
this does not imply full Windows server restore support.

[#58](static-cache-performance.md) measured static serving before adding bounded
configuration reuse. The documented warm small-file results are not end-to-end
server throughput. Concurrency and live access/pointer tests accompany the cache.

[#59](rewrite-boundary-review.md) deliberately retains production validation.
A directory target can generate a canonical-slash 301 after a 200 rewrite, so
removing every terminal rewrite edge is not justified. Tests and a concrete
counterexample complete the conditional assessment; no skipped test substitutes
for that conclusion.

## Final verification

Local compiler: `go version go1.27.1 darwin/arm64`, `GOTOOLCHAIN=local`.
All commands below run on the source commit stated above. Counts refer to
**top-level test functions**, excluding their subtests; packages without tests
are not counted as test passes.

| Check | Command / scope | Result |
| --- | --- | --- |
| Build | `CGO_ENABLED=0 go build ./...` | PASS |
| Vet | `CGO_ENABLED=0 go vet -tags=integration ./...` | PASS |
| Default suite | `CGO_ENABLED=0 go test -json -count=1 ./...` | 244 passed; 1 optional footprint test skipped; 10 test packages |
| Integration | `CGO_ENABLED=0 go test -json -tags=integration -count=1 ./...` | 299 passed; 3 optional tests skipped; 10 test packages |
| Race | `CGO_ENABLED=1 go test -race -json -tags=integration -shuffle=on -count=1 ./...` | 299 passed; 3 optional tests skipped; 10 test packages; no races |
| Original release targets | `make build-all` | PASS: Darwin arm64/amd64 CLI, Linux amd64 CLI/server, Windows amd64 CLI; pure-Go binaries, formats inspected |
| Installer smoke | `sh scripts/setup_test.sh` | PASS: four TLS modes, configuration preservation, upgrade backup, ports and both generated downloads |
| Website downloads | `make check-website` | PASS |
| Actual Chromium | Explicit browser environment and `test-required.sh TestConsoleBrowser ./internal/server` | PASS; real Chromium executed |
| Controlled ACME | `sh scripts/test-acme.sh` | PASS; local Pebble HTTP/DNS challenge validation and race instrumentation executed |
| New-directory restore | `test-required.sh TestRestoreRealHTTPAndTLSAccessDomainsAndRollback ./internal/backup` | PASS; real HTTP/TLS, access rules, domains and rollback |
| Historical migrations | Integration suite's fixed-schema store and server upgrade/restore tests | PASS |

The ordinary integration/race suites skip `TestConsoleBrowser`, `TestLocalACME`
and the opt-in performance diagnostic `TestRestoreFootprint`. Browser and ACME
were separately enabled and passed as shown above. Footprint measurements belong
to #57's recorded benchmark experiment and are not counted as a final-suite pass.
Final PR CI repeats Linux integration/race/browser plus native Windows staging.
The [baseline](compatibility-baseline.md) contains reproducible acceptance
commands and required-test guards that reject a skip or zero matching tests.

Local tests use temporary data, controlled services and fake systemd for installer
contracts. They do not validate a production DNS zone, public CA account or a
particular distribution's service permissions. Restore tests do validate actual
new-directory file/SQLite state and real listeners. Follow the
[operations guide](operations-m3.md) for deployment and backup/rollback steps.
