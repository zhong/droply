# M3 management audit

Audit records are stored in SQLite alongside the managed state. Each authenticated management operation first commits a `pending` record; an unavailable audit store returns 503 before the operation starts. Handlers report numeric resource targets and semantic `success` or `failure` explicitly; response bodies are never inspected. Completion records the actual HTTP status (0 when the request is aborted before any final response). A failed domain verification or partially failed cleanup is a failure even when its API returns 200. An uncertain commit, an operation without an explicit result, a crash, or a failed final audit write leaves `pending`: inspect the target's current state before retrying. A confirmed operation remains successful even if sending its response fails. Audit completion is not a distributed transaction with the operation, so pending does not imply that no change happened.

Records identify the user or project token, project/subdomain, action, numeric target and UTC timestamp. They do not store request bodies, passwords, bearer tokens, invitation values, cookie values or DNS ownership proofs. Login failures are throttled but are not management audit records. Access rules for visitors are independent of management roles.

Audited operations include deployment, promotion, rollback, artifact cleanup, domain binding/verification/removal, access rules, membership, project token creation/revocation, invitations and project deletion. Subdomain access changes appear in affected projects' audit queries. Deleted resources remain visible to an administrator through the global query.

```sh
droply audit --sub team --project docs --limit 50
droply audit --sub team --project docs --limit 50 --before 123
droply audit --admin --limit 100
```

Project members can read that project's audit through `GET /subdomains/{sub}/projects/{project}/audit`; administrators can read `GET /admin/audit`. Project CI tokens retain their narrow deployment scope and cannot read audit records. Removed members immediately lose query access. Both endpoints return `{events, next_cursor}` in descending event ID order. Pass `next_cursor` as `before` for the next page; zero means no next page. Limits are 1–100, default 50.

The server's `--audit-retention-days` defaults to 90 and must be positive. Daily maintenance deletes older records. This is a local operational history, not an append-only compliance archive: the database administrator can change it, and a full backup contains its records. See [backup and restore](backup-m3.md).
