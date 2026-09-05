# M0 migration: standalone Droply / 单服务迁移

M0 makes Droply responsible for HTTP routing, static files, access control and HTTPS. User accounts, API tokens, access rules, projects, files and existing `subdomain.base-domain/project` URLs remain in the existing database and sites directory. It does not add deployment history, previews or rollback of published content; those belong to later milestones.

The installer does not rewrite an existing service or gateway. `UPGRADE=1` only backs up and replaces the binary and does not restart it. The operator chooses the entry point and performs the switch. The instructions below assume `/data/droply`, `/usr/local/bin/droply-server` and `droply.service`; substitute your actual paths and service name.

## 1. Inventory and consistent backup

Record `systemctl cat droply`, the running binary version, data path, service user, environment files, explicit `--hmac-secret`, WeCom credentials/callback, DNS records, certificate storage and listening ports (`ss -ltnp`). Preserve the signing key and WeCom settings. New installations persist an automatically generated key in `data-dir/hmac.key`; an existing explicit signing secret must continue to be supplied to preserve its sessions.

Stop only the Droply writer while taking a consistent snapshot. Do not stop a shared gateway during backup:

```bash
backup=/var/backups/droply/pre-m0-$(date +%Y%m%d-%H%M%S)
sudo mkdir -p "$backup"
sudo chmod 700 "$backup"
sudo systemctl stop droply
sudo tar -C / -cpf "$backup/state.tar" \
  data/droply etc/systemd/system/droply.service usr/local/bin/droply-server
sudo chmod 600 "$backup/state.tar"
```

Also copy any service drop-ins, environment files and gateway configuration/certificate directories into this private backup. These paths differ by installation; inspect them first. Include SQLite `-wal`/`-shm` files if present by archiving the entire data directory. Verify the archive with `sudo tar -tf "$backup/state.tar"` before proceeding. If you need more preparation time, restart the old Droply service now and take a fresh snapshot immediately before the eventual switch.

Do not launch old and new writers against the same database. To rehearse, extract a snapshot into a different private directory and run the new binary with that directory and unused high ports.

## 2. Install the binary and choose an entry point

```bash
sudo env UPGRADE=1 LOCAL_BINARY=/path/to/new/droply-server sh scripts/setup.sh
```

This creates an additional timestamped backup under `/var/backups/droply`, preserves the service/environment and does not restart. The installer never overwrites `hmac.key`, certificate directories, custom configuration or an existing data directory.

### Keep your existing gateway

Use the new unified HTTP listener behind the gateway:

```text
--addr 127.0.0.1:8080 --tls-mode http --trusted-proxies 127.0.0.1/32
```

Proxy both API and site/custom-domain requests to that listener while preserving the original `Host`. Configure the trusted proxy CIDRs for the gateway's real source address. Never trust all networks merely to accept forwarded headers. The old `--site-addr 127.0.0.1:8081` can temporarily preserve a second upstream listener during the transition; it now handles both API and sites. `--caddy-admin` is accepted but ignored.

An existing gateway's direct `file_server` routes **must be changed by the operator** to proxy through Droply. Otherwise those requests still bypass access control and domain verification. The application cannot discover or rewrite an arbitrary shared proxy configuration. Review only Droply-owned routes, validate the gateway configuration using its normal procedure, and keep unrelated routes intact.

### Let Droply own 80/443

Run `ss -ltnp` and identify both ports' current owners. If a shared gateway owns them, choose a separate IP/host or keep the gateway. Only after deciding that the relevant listener can be removed should the operator stop or reconfigure that listener. The setup script never performs this step.

Edit the existing Droply unit (`sudo systemctl edit --full droply`), retaining its user, data path, environment and WeCom flags. Use one of:

```text
--addr :80 --https-addr :443 --tls-mode auto --acme-email admin@example.com
--addr :80 --https-addr :443 --tls-mode cloudflare --cloudflare-token-file /etc/droply/cloudflare-token
--addr :80 --https-addr :443 --tls-mode manual --tls-cert /etc/droply/cert.pem --tls-key /etc/droply/key.pem
```

Retain `--domain` and `--data-dir`. Remove the legacy `--site-addr`, `--caddy-admin` and `After=caddy.service` entries once unused. A non-root service needs `AmbientCapabilities=CAP_NET_BIND_SERVICE` and `CapabilityBoundingSet=CAP_NET_BIND_SERVICE` to bind low ports. Ensure the existing service user can read its credentials and write the data/certificate directory; do not recursively change ownership of shared gateway storage.

Then run:

```bash
sudo systemctl daemon-reload
sudo systemctl start droply
sudo systemctl status droply
sudo journalctl -u droply -n 100 --no-pager
```

Normal HTTP shutdown drains requests for up to 15 seconds. An in-flight DNS/ACME operation may delay process exit until its library timeout (up to five minutes); allow at least `TimeoutStopSec=360` in systemd.

An address conflict, malformed/expired manual certificate or unreadable credentials causes startup to fail. Correct the configuration or restore the old entry point; a successful `systemctl` invocation alone does not prove issuance or site availability.

## 3. Certificates and DNS ownership

Droply does not import Caddy's account/cache format. Choose either:

- **Reissue:** use `auto` or `cloudflare`. Keep the old certificate storage untouched. New ACME accounts/certificates persist under `data-dir/certificates` or `--cert-dir`. Rehearse with an ACME staging directory (`--acme-ca`) and a separate certificate directory to avoid production issuance limits. Staging certificates are not browser-trusted.
- **Manual PEM import:** copy the required full certificate chain and matching private key to a dedicated private directory readable by the Droply service user. Set `--tls-cert` and `--tls-key`. Do not move or change the gateway's originals. The certificate must cover the API hostname and each HTTPS site you expose; a wildcard covers only one label. Manual renewal remains the operator's responsibility, followed by a service restart.

Cloudflare DNS mode accepts `--cloudflare-token-file` or `DROPLY_CLOUDFLARE_API_TOKEN`. Use a token with `Zone:DNS:Edit` and `Zone:Zone:Read` for the required zones. Keep it in a restricted file; do not put its value in `ExecStart` or shell history. A base-zone token does not grant control over an unrelated custom domain's zone. Base-domain wildcard certificates cover platform subdomains; custom-domain certificates still need authorization and a supported challenge for their own domain.

Existing **verified** domain records remain verified and keep serving after restart. Existing **pending** records receive a persistent, dedicated TXT challenge. New bindings return:

```text
status: pending
verification_record: _droply-verification.blog.example.com
verification_token: <unique value printed by Droply>
```

Publish that exact TXT value, then run `droply domain verify blog.example.com --sub alice --project blog`. A matching A or CNAME record alone is no longer ownership proof. Failed DNS lookup or persistence does not report success; retry after correcting the cause. Unbinding or deleting a project revokes serving and certificate authorization. Rebinding generates a new challenge, so the old TXT value cannot verify it.

The migration lowercases stored hostnames and removes trailing dots/whitespace. If previously distinct rows collapse to the same hostname, startup stops with a normalization/uniqueness error. Restore the backup or resolve the ownership conflict explicitly in a reviewed copy of the database; do not delete an arbitrary binding to make startup succeed.

## 4. Validate before removing the old entry point

Check all of these using your real hostnames:

- API login and an existing API token still work; project lists, content and old URLs match the snapshot.
- Both the old subdomain path and a verified custom domain return the expected content.
- An unauthenticated private-site request is denied or asks for login; an allowed visitor succeeds; private responses include `Cache-Control: private, no-store`.
- A subdomain session cannot bypass a stricter project rule. WeCom callbacks reach the configured callback host.
- An unknown or pending custom domain returns 404 and cannot obtain a certificate. Deleting a binding revokes access.
- HTTPS presents a valid chain and correct SAN for the API and selected site. Check issuance/renewal status using `droply certificate <domain>` with an authenticated CLI context.
- Restart Droply once, then repeat API, public/private site and HTTPS checks to confirm persistence.

Keep the old gateway configuration, old binary and backup until this validation succeeds. No installer step uninstalls Caddy or edits other applications' routes.

## 5. Roll back an unsuccessful switch

Stop the new Droply process first. If you assigned its ports to Droply, release them before restarting the old gateway. Retain a copy of the new state for diagnosis:

```bash
sudo systemctl stop droply
sudo mv /data/droply /data/droply.failed-m0
sudo tar -C / -xpf "$backup/state.tar"
sudo systemctl daemon-reload
```

Choose a unique `droply.failed-m0` path if it already exists. Restore your separately saved service drop-ins/environment and only the gateway routes you changed. Start the old gateway if you stopped it, then start the restored Droply service and verify old URLs/private access. Restoring the snapshot discards writes made after that snapshot; prevent deployment writes during the cutover or reconcile them before rollback. Do not automatically restore an entire shared gateway configuration that other applications may have changed meanwhile.

## 中文操作要点

先停止 Droply 写入并备份整个数据目录、旧二进制、服务和环境配置，再选择“保留现有网关”或“Droply 接管端口”。脚本不会卸载共享代理，也不会自动修改未知代理路由。`UPGRADE=1` 只备份替换二进制，不覆盖配置或重启。

保留网关时，所有 Droply 站点必须改为反代到统一入口，避免旧的直接读文件配置绕过访问控制。直接监听 80/443 时先明确端口归属，再由管理员切换。保留原用户、token、访问规则、内容目录、HMAC 密钥和企业微信参数。

旧已验证域名保持有效，旧未验证域名与新绑定需要专属 TXT 挑战。规范化后冲突的域名会阻止启动，需要人工确认归属。证书选择重新签发或导入 PEM，原 Caddy 证书目录保持不动。失败时停止新服务，恢复一致备份及自己修改的入口，重新验证旧 URL 和私有访问。

## Repository verification

`sh scripts/setup_test.sh` runs isolated local-binary installations for all four modes with fake `systemctl`/`ss`, checks occupied ports 80/443, existing configuration preservation and binary-upgrade backups. It never needs Caddy or real systemd. Go tests exercise persisted domain migration/restart and local HTTPS/access-control behavior. A real production DNS zone, public ACME issuance and distribution-specific systemd remain deployment acceptance checks.
