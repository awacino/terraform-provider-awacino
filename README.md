# Terraform Provider for Awacino

The official [Terraform](https://www.terraform.io) / [OpenTofu](https://opentofu.org) provider for the
[Awacino API](https://www.awacino.com): automate an Awacino company account.

- `awacino/awacino` in the [Terraform Registry](https://registry.terraform.io/providers/awacino/awacino)
- Built only on the public API, `https://api.awacino.net/v1`
- [Documentation](https://registry.terraform.io/providers/awacino/awacino/latest/docs) (generated from this repository's `docs/`)

| Resource | What it manages |
|---|---|
| `awacino_member` | A member account and its first invitation code (offboarded on destroy) |
| `awacino_invitation` | A new invitation code for an invited member |
| `awacino_beamin_device` | A server's one-time setup code + install lines (`cloud_init`, `docker`, `apt`); the registered Beam-in device |
| `awacino_access_grant` | A member's access to one Beam-in device (reach, end date) |
| `awacino_beamin_code` | A Beam-in code for a Beam-in device (reach, internet, end date, label, paused) |

Data sources: `awacino_company`, `awacino_members`, `awacino_member`, `awacino_devices`, `awacino_device`,
`awacino_active_connections`, `awacino_api_key`. Every resource supports `terraform import`.

## Quick start

1. In the Awacino app, open **Company settings > Awacino API > New API key** and tick **Users** and **Connections**
   (Read and Write). The secret is shown once.
2. Export the key and write a configuration:

```bash
export AWACINO_KEY_ID=awk_...
export AWACINO_KEY_SECRET=aws_...
```

```terraform
terraform {
  required_providers {
    awacino = {
      source  = "awacino/awacino"
      version = "~> 0.1"
    }
  }
}

provider "awacino" {}

resource "awacino_member" "dieter" {
  email        = "dieter@company.example"
  display_name = "Dieter Muster"
  external_id  = "4711"
  role         = "read"
  send_invitation = { days = 7, locale = "de" }
}

data "awacino_device" "nas" {
  name = "Office Hamburg - NAS"
}

resource "awacino_access_grant" "dieter_nas" {
  member_id   = awacino_member.dieter.id
  device_id   = data.awacino_device.nas.id
  reach       = { kind = "network" }
  valid_until = "2027-06-30T00:00:00Z"
}
```

3. `terraform init && terraform apply`.

Full examples: [`examples/onboard-office`](examples/onboard-office/main.tf) (a new office: members, the office server's
setup code, grants, a Beam-in code for the IT partner), [`examples/hoster`](examples/hoster/main.tf) (a cloud server and a
Beam-in code for its customer) and [`examples/managed-service-provider`](examples/managed-service-provider/main.tf).
Without Terraform: [`examples/cloud-init`](examples/cloud-init/cloud-config.yaml) and [`examples/ansible`](examples/ansible/enrol.yml).

## Provider settings

| Argument | Environment | |
|---|---|---|
| `key_id` | `AWACINO_KEY_ID` | API key id (`awk_...`) |
| `key_secret` | `AWACINO_KEY_SECRET` | API key secret (`aws_...`), sensitive |
| `endpoint` | `AWACINO_ENDPOINT` | Default `https://api.awacino.net` |

How the provider talks to the API:
- it exchanges the key at `POST /v1/oauth/token` (OAuth 2.0 client credentials) for a 15-minute bearer token and renews it
  two minutes before it ends; while the token endpoint is throttled it sends the key headers instead;
- every write carries an `Idempotency-Key` that stays the same on retries, so a retry never creates a second object;
- `429` and `5xx` answers are retried with exponential back-off (1 to 30 s, honouring `Retry-After`);
- errors are RFC 9457 problem details; the diagnostic names the code, what to do, and the request id for support;
- a key without the needed section fails early with the checkbox to tick ("This key has no Connections section with
  Write access").

## Codes and the state

Invitation codes, setup codes and Beam-in codes are returned by the API **once**, on create, and kept in the state as
sensitive values. Keep the state in an encrypted backend with restricted access. An imported resource has no code.

## A server created in the same configuration

`wait_for_device = true` on `awacino_beamin_device` waits until the server registered. Do not combine it with a server
that gets `cloud_init` in the same configuration - that server is only created after the setup code resource is done. Use
the `awacino_device` data source with `wait_timeout` and `depends_on` on the server instead (see
[`examples/hoster`](examples/hoster/main.tf)).

## Development

Requirements: Go (see `go.mod`), Terraform >= 1.5.

```bash
make build        # go build ./...
make test         # unit tests: an in-memory mock of the API, and the examples through the real Terraform CLI
make testacc      # acceptance tests against a real Awacino company (see below)
make docs         # regenerate docs/ with tfplugindocs (from the schema, examples/ and templates/)
make snapshot     # local GoReleaser build of all release archives, unsigned
```

Acceptance tests need a dedicated test company with an API key (Users + Connections, Read + Write) and one Beam-in device:

```bash
export AWACINO_KEY_ID=awk_... AWACINO_KEY_SECRET=aws_... AWACINO_TEST_DEVICE_ID=...
make testacc
```

Every object they create is named `tfacc-...` and destroyed at the end of its test.

Use a local build with Terraform through `dev_overrides` in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "awacino/awacino" = "/path/to/go/bin"
  }
  direct {}
}
```

Releases: a `v*` tag runs `.github/workflows/release.yml` (GoReleaser, GPG-signed `SHA256SUMS`, Registry manifest).

## License

[MPL-2.0](LICENSE)
