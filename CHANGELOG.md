# Changelog

All notable changes to this provider. The format follows [Keep a Changelog](https://keepachangelog.com); versions
follow [SemVer](https://semver.org) (`0.x` while the API is new).

## 1.0.0 (20 November 2026)

First release.

Resources:
- `awacino_member` - members with role `read`, `user` or `auditor`, display name, `external_id`, rights for role `read`,
  activate / deactivate, the first invitation code (optionally mailed by Awacino). Destroy offboards. Import by id or
  `external_id:<value>`; a changed e-mail address replaces the member, a change of case does not.
- `awacino_invitation` - a new invitation code for an invited member; `triggers` issue another one. A code revoked
  outside Terraform is issued again on the next apply.
- `awacino_beamin_device` - a one-time setup code for an unattended server with `cloud_init`, `docker` and `apt` install
  lines; `wait_for_device` waits for the registration; `name` renames the registered device. Destroy removes the device
  or revokes the unused code. Import by enrolment id or device id.
- `awacino_access_grant` - a member's access to one Beam-in device (`reach`, `valid_until`; `internet` once the server
  enforces it). Import by grant id.
- `awacino_beamin_code` - a Beam-in code (`reach` network / device / ports, `internet`, `valid_days` or `expires_at`,
  label, `external_id`, `paused`); `code` and `connect_url` once, sensitive. A new reach, internet, device or lifetime
  makes a new code. Import by code id.

Data sources: `awacino_company`, `awacino_members`, `awacino_member`, `awacino_devices`, `awacino_device` (with
`wait_timeout` for a server that registers during the apply), `awacino_active_connections`, `awacino_api_key`.

Provider: key id + secret (or `AWACINO_KEY_ID` / `AWACINO_KEY_SECRET`), OAuth client-credentials bearer token renewed
before it ends, `Idempotency-Key` on every write, retries with back-off on `429` / `5xx` (honouring `Retry-After`),
RFC 9457 errors with hints and request ids, an early check of the key's sections.
