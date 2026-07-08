# tfstateref-lint

Lint `data "terraform_remote_state"` references against the actual tfstate files.

A reference to a state file or output that doesn't exist is only detected when `terraform plan` runs. This tool reads the real state files with [tfstate-lookup](https://github.com/fujiwara/tfstate-lookup) and reports broken references before that.

Checks:

- state-existence: the state referenced by each `data "terraform_remote_state"` block exists
- output-reference: each `data.terraform_remote_state.X.outputs.Y` reference (nested paths, `lookup()`, same-file local aliases) exists in the state

Supported backends: `s3`, `gcs`, `azurerm`, `remote` (Terraform Cloud / Enterprise), `local`, `http`.

## Install

```bash
go install github.com/takaishi/tfstateref-lint/cmd/tfstateref-lint@latest
```

## Usage

Credentials for the backend are required to read the state files (e.g. AWS credentials for `s3`, `TFE_TOKEN` for `remote`).

```bash
# lint ./terraform/ (default)
tfstateref-lint

# lint specific directories
tfstateref-lint terraform/service_a/staging terraform/service_b/staging

# JSON output
tfstateref-lint --json -q terraform/ | jq .
```

Logs go to stderr. With `--json`, results go to stdout:

```json
{
  "dirs": ["terraform/"],
  "errors": [
    {
      "file": "terraform/service_a/staging/app/remote_state.tf",
      "message": "terraform_remote_state \"network\" references s3://example-terraform-state/env/network/terraform.tfstate, but the state could not be read"
    }
  ],
  "state_existence": { "checked": 12, "errors": [] },
  "output_reference": { "checked": 87, "errors": [] }
}
```

Exit codes: 0 = no errors, 1 = lint errors found, 2 = usage error.

## GitHub Actions

```yaml
name: tfstateref-lint

on:
  pull_request:
    paths:
      - "terraform/**/*.tf"

permissions:
  id-token: write
  contents: read

jobs:
  tfstateref-lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::123456789012:role/tfstateref-lint
          aws-region: ap-northeast-1
      - run: go install github.com/takaishi/tfstateref-lint/cmd/tfstateref-lint@latest
      - run: tfstateref-lint terraform/
```

The IAM role only needs `s3:GetObject` on the tfstate bucket. If state buckets live in multiple AWS accounts, split the job with a matrix and assume a role per account.

## Limitations

- `backend`, `workspace` and `config` values must be literals; blocks using variables are skipped
