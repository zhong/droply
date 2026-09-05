# Unattended deployment

Build the site's files in your own CI runner, then upload that directory with a CLI built from the M2 source. Droply does not run your build or execute Functions/Workers.

## Credentials and configuration

The owner first creates the project with a deployment, then creates separate project credentials:

```sh
droply project-token create --sub alice --project blog --name ci-preview --scope preview
droply project-token create --sub alice --project blog --name ci-production --scope production
```

Each command prints JSON containing `token` exactly once. Save it directly in the CI secret store; do not commit it. Defaults and revocation are documented in [project tokens](project-tokens-m2.md).

Set `DROPLY_API_URL` to the API origin (for example `https://api.example.com`) and `DROPLY_TOKEN` to the appropriate credential. Environment variables override each connection field independently. The underlying context is selected by `--context`, then `.droply.toml`'s `context`, then saved `current_context`. An explicitly empty token clears the saved token. The API URL defaults to the public endpoint if empty. Deployment and history commands never save the effective configuration; legacy configuration is migrated in memory and only persisted by an explicit configuration/login operation.

The target comes from `--sub` and `--project`, falling back to `.droply.toml` in the working directory. Environment credentials do not override the project identity. A project token cannot create another project or modify access policy.

```sh
# The runner supplies DROPLY_API_URL and a preview-scoped DROPLY_TOKEN.
droply deploy ./dist --sub alice --project blog \
  --preview --branch "$CI_BRANCH" --commit "$CI_COMMIT" --json > deployment.json

# A separately authorized production job uses its production credential.
droply deployment promote 42 --sub alice --project blog --json > promotion.json
# Or upload a fresh production artifact:
droply deploy ./dist --sub alice --project blog --json > deployment.json
```

`deploy --json` writes one JSON object on success, including the version and URLs returned by the server. Packaging and upload progress go to stderr. Failures exit nonzero and do not emit success JSON. History, promotion, rollback and token commands support machine-readable output (`deployment events` and token commands always output JSON). A successful publication response records the transaction's result; a concurrent later publisher may already have replaced production by the time you read it.

## Retry contract

Uploads have **no automatic retries**. The CLI refuses HTTP redirects and disables replay of the multipart body. Configure the final API URL, including the correct HTTPS scheme. A connection loss or an unreadable response can occur after the server commits a deployment: inspect `droply deployment list --json` and correlate branch/commit before manually retrying. A fresh upload allocates a fresh version; repeating the same commit is not an idempotency key. Promotion of the version already in production returns `changed: false`; a repeated promotion after another publication is a new production switch.

## GitHub Actions example

This example assumes a Node build that emits `dist/`. Replace the build steps for your own toolchain. Configure `DROPLY_API_URL` and `DROPLY_VERSION` as repository variables; the latter must name a reviewed M2 commit SHA or a release containing M2. The example builds that pinned CLI from source. Configure the two tokens as separate secrets. The production environment can enforce your organization's approval rules. This example runs only branch pushes within the repository and does not expose deployment credentials to forked pull requests.

```yaml
name: Publish static site
on:
  push:
    branches: ['**']
permissions:
  contents: read
jobs:
  preview:
    if: github.ref_name != 'main'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: npm
      - run: npm ci && npm run build
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27.1'
      - run: |
          test -n "$DROPLY_VERSION"
          go install "github.com/zhong/droply/cmd/droply@$DROPLY_VERSION"
        env:
          DROPLY_VERSION: ${{ vars.DROPLY_VERSION }}
      - run: droply deploy dist --sub alice --project blog --preview --branch "$CI_BRANCH" --commit "$CI_COMMIT" --json > deployment.json
        env:
          DROPLY_API_URL: ${{ vars.DROPLY_API_URL }}
          DROPLY_TOKEN: ${{ secrets.DROPLY_PREVIEW_TOKEN }}
          CI_BRANCH: ${{ github.ref_name }}
          CI_COMMIT: ${{ github.sha }}
  production:
    if: github.ref_name == 'main'
    environment: production
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: npm
      - run: npm ci && npm run build
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27.1'
      - run: |
          test -n "$DROPLY_VERSION"
          go install "github.com/zhong/droply/cmd/droply@$DROPLY_VERSION"
        env:
          DROPLY_VERSION: ${{ vars.DROPLY_VERSION }}
      - run: droply deploy dist --sub alice --project blog --commit "$CI_COMMIT" --json > deployment.json
        env:
          DROPLY_API_URL: ${{ vars.DROPLY_API_URL }}
          DROPLY_TOKEN: ${{ secrets.DROPLY_PRODUCTION_TOKEN }}
          CI_COMMIT: ${{ github.sha }}
```
