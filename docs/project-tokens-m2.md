# Project tokens for CI

Project tokens grant access to one existing project. Create and manage them using the project owner's ordinary user token:

```sh
droply project-token create --sub alice --project site --name github-actions
droply project-token create --sub alice --project site --scope preview,production --expires-in 720h
droply project-token list --sub alice --project site
droply project-token revoke 1 --sub alice --project site
```

Commands fall back to `.droply.toml` for the subdomain and project. Creation prints JSON containing `token` exactly once. Store that value in the CI provider's secret store; list responses contain metadata only. The server stores a SHA-256 digest, not the credential. Avoid printing the creation response in public build logs.

The default is `preview` scope and a 30-day lifetime. `preview` permits preview uploads; `production` permits production uploads, promotion, and rollback. Specify both scopes if the pipeline needs both. Both scopes permit reading deployment history and events for the same project. Tokens cannot create projects, manage credentials, edit domains/access rules, clean up artifacts, or access another project. Expiration must be in the future and no more than 365 days away. Revocation takes effect for subsequent authenticated requests; already authenticated in-flight operations may finish. Existing user tokens continue to work.

The owner-only API is `POST /subdomains/:sub/projects/:project/tokens` with optional `name`, `scopes` and RFC3339 `expires_at`; `GET` lists metadata and `DELETE /tokens/:id` revokes. Revocation is idempotent. Project deletion invalidates its credentials.
