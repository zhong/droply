# Private installation and invitations

New installations disable public registration. Existing accounts and API credentials continue to work after upgrading; upgrading does not silently make an existing account an administrator. Site visitor passwords and WeCom authorization remain separate from publisher accounts.

## Local administrator setup

Stop the server before either command. They take the same exclusive data-directory lock as the server and expose no network bootstrap endpoint. On a new installation, create a private password file, then run:

```sh
chmod 600 /secure/initial-password
droply-server init-admin --data-dir /data/droply \
  --email admin@example.com --password-file /secure/initial-password
```

The password must contain 8–72 bytes; a final line ending is stripped. This command works only while the installation has no administrator and does not reset an existing password. Existing ordinary users do not prevent creating a separate administrator account. Remove the temporary password file after securely recording the account credentials. Start the service and use `droply login --api-url https://api.example.com`.

For an existing installation, explicitly select an existing account while the server is stopped:

```sh
droply-server claim-admin --data-dir /data/droply --email owner@example.com
```

This preserves the user's existing password, token and project ownership. Local access to the data directory is the authority for this recovery operation. This is an upgrade path for a database that has no administrator yet; it refuses to run when an administrator already exists. A restored backup preserves administrator identities and passwords. Keep those credentials securely; this command is not a password-reset mechanism.

## Invite an account

An administrator uses their authenticated CLI:

```sh
droply invitation create colleague@example.com --expires-in 24h
droply invitation list
droply invitation revoke 1
```

Creation prints the invitation token once as JSON. Deliver it through your own private channel. The recipient sets `DROPLY_INVITE` to the received token and runs `droply register --api-url https://api.example.com`, entering the invited email and a password. Invitations are bound to the email, expire, can be revoked and can be consumed only once. Invitation metadata can be listed without exposing the token.

Public registration requires the explicit server option `--open-registration` or `DROPLY_OPEN_REGISTRATION=true`. Omit it for private operation. This setting does not alter existing users or visitor access rules. Failed account authentication is throttled; retry after the response's rate-limit delay rather than repeatedly resubmitting.
