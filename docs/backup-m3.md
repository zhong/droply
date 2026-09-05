# 离线备份与恢复（M3）

备份包含整个 data-dir（SQLite 改为一致性快照，运行锁和 SQLite 临时日志除外），因此包括生产及历史 artifact、域名/访问规则/账号/审计数据、默认 HMAC 密钥、位于 data-dir 内的证书及 ACME 账户。所有可用 deployment 的 artifact 必须通过自身 manifest 和文件散列检查，否则备份失败。

## 备份

先停止服务及其他会写入配置、证书或 data-dir 的进程。命令全程持有服务器相同的 data-dir 排他锁；服务器仍运行时明确失败。外部证书目录与配置文件同样必须停止写入。不能使用此命令热备份。

```sh
sudo systemctl stop droply
./droply-server backup --data-dir /data/droply \
  --output /backups/droply-2026-09-05.tar.gz \
  --config /etc/systemd/system/droply.service \
  --config /etc/droply/environment \
  --include /etc/droply/certificates
sudo systemctl start droply
```

至少一个 `--config` 必填；可重复传入实际生效的 systemd unit、EnvironmentFile、容器 compose 配置等。程序无法推导外部配置引用的全部文件：必须用可重复的 `--include` 明确添加外部 `--cert-dir`、手动 TLS PEM 文件及任何被配置引用的密钥文件。默认 cert-dir 若在 data-dir 内会自动包含。命令不展开 shell 变量，不读取运行进程环境，也不上传备份。

默认 `hmac.key` 必须为有效的 32 字节密钥。如果使用 `--hmac-secret` 覆盖默认密钥，必须传入 `--hmac-key-file /private/active-key`，其中保存精确密钥字节（不要加换行）。恢复后仍应配置同一显式 HMAC 值；`hmac.override` 不会自动取代 `hmac.key`。漏传有效覆盖值会使原会话失效。

SQLite 通过 `VACUUM INTO` 创建真正的一致性快照，包含 WAL 中已提交的数据，不迁移或升级源数据库。随后校验完整性、外键和引用 artifact。快照、复制文件、目录及最终输出均同步落盘。只有完成后才以不覆盖已有文件的方式发布压缩包；失败时原数据库不变且不发布输出。此协议依赖所有 Droply 写入者遵守排他锁，外部管理操作遵守停止写入要求。

## 恢复与演练

目标必须是尚不存在的新目录，其父目录应由管理员控制且已存在。不会覆盖原安装，即使目标是空目录也会拒绝。预留至少解压数据量的两倍临时磁盘空间。

```sh
./droply-server restore --input /backups/droply-2026-09-05.tar.gz \
  --data-dir /data/droply-restored
```

默认最多解压 64 GiB、100000 个条目；较大备份可显式调整 `--max-bytes` 和 `--max-files`。解压严格拒绝绝对路径、路径穿越、符号链接/硬链接与特殊文件；校验压缩流结尾、manifest 版本、每个文件 SHA-256/大小、目录及文件完整集合、数据库版本/完整性/外键、引用 artifact。未知备份版本或高于支持上限的数据库拒绝恢复。完整验证通过后才原子发布新 data-dir。

格式版本为 1；数据库 `user_version` 0（早期未标版本）至 3 可接受，必须与 manifest 一致。启动服务器时才执行其正常版本迁移。

外部材料恢复到 `.restore/<restore-id>/external/...`，对应清单在 `.restore/<restore-id>/manifest.json`：`source` 记录旧绝对路径，`path` 相对新的 data-dir。不会写入旧绝对路径。根据最新清单，把复制的服务配置中的 `--data-dir`、`--cert-dir`、`--tls-cert`/`--tls-key` 等实际参数改到新位置；保留域名、端口、DNS 凭证和 HMAC 的实际值。**手动证书与外部 cert-dir 必须重配路径后再启动。** 多次备份恢复保留早期清单，新清单位于新 restore-id。

在隔离机器或测试端口启动恢复服务，检查生产内容、一个旧版本回滚、密码/IP 保护、原会话、绑定域名及 TLS 证书；确认后再切换流量。恢复单元/集成测试覆盖 WAL 快照、损坏/版本拒绝、真实 HTTP/TLS、原密码 Cookie、域名和 HTTP API 回滚。

压缩包和恢复清单可能包含服务配置、访问密钥、密码哈希、HMAC、证书私钥与 ACME 账户。输出及复制文件使用 0600、目录 0700；压缩包未加密。按密钥材料存储，并使用组织既有的加密离线存储和传输方式。SHA-256 用于检测损坏，不是对不可信来源的数字签名。

参考：[SQLite VACUUM INTO 一致性与持久性说明](https://www.sqlite.org/lang_vacuum.html)。
