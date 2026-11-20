# Onboard a new office with Awacino:
#   1. every employee of the office becomes a member (role read) and gets the invitation mail,
#   2. the office server gets a one-time setup code and a cloud-init document,
#   3. every employee may use the office's Beam-in devices (the NAS, ...) and their network,
#   4. the IT partner gets a 30-day Beam-in code for remote login and HTTPS on the NAS only.
#
# Needs an API key with the sections Users and Connections (Read and Write) and the Management package
# (Standard or Premium). Run:  terraform init && terraform apply

terraform {
  required_version = ">= 1.5"
  required_providers {
    awacino = {
      source  = "awacino/awacino"
      version = "~> 1.0"
    }
  }
}

# AWACINO_KEY_ID and AWACINO_KEY_SECRET from the environment or your CI's secret store.
provider "awacino" {}

variable "office" {
  description = "Name of the new office; used in labels and ids."
  type        = string
  default     = "Hamburg"
}

variable "employees" {
  description = "The office's people, by employee number (becomes the member's external_id)."
  type = map(object({
    email  = string
    name   = string
    locale = optional(string, "de")
  }))
  default = {
    "4711" = { email = "dieter.muster@company.example", name = "Dieter Muster" }
    "4712" = { email = "erika.beispiel@company.example", name = "Erika Beispiel" }
  }
}

variable "office_devices" {
  description = "Names of the office's existing Beam-in devices (set up in the Awacino app) every employee may use."
  type        = list(string)
  default     = ["Hamburg - NAS"]
}

variable "it_partner_device" {
  description = "The Beam-in device the IT partner may reach (remote login + HTTPS only)."
  type        = string
  default     = "Hamburg - NAS"
}

data "awacino_company" "this" {}

check "enough_seats" {
  assert {
    condition     = data.awacino_company.this.seats_total - data.awacino_company.this.seats_used >= length(var.employees)
    error_message = "Not enough free user seats for the new office - add users on awacino.com first."
  }
}

# ---------------------------------------------------------------- 1. the people

resource "awacino_member" "employee" {
  for_each = var.employees

  email        = each.value.email
  display_name = each.value.name
  external_id  = each.key
  role         = "read" # sees only the devices granted below

  send_invitation = {
    days   = 7
    locale = each.value.locale
  }
}

# ---------------------------------------------------------------- 2. the office server

resource "awacino_beamin_device" "office_server" {
  label       = "${lower(var.office)}-server-01"
  external_id = "office-${lower(var.office)}-server-01"
  hours       = 24
}

# Hand the setup code to the machine that installs the server - for example as user data:
# resource "hcloud_server" "office" {
#   name        = "${lower(var.office)}-server-01"
#   server_type = "cx22"
#   image       = "debian-12"
#   user_data   = awacino_beamin_device.office_server.cloud_init
# }
# and, once it registered (same apply), grant it as well:
# data "awacino_device" "office_server" {
#   external_id  = awacino_beamin_device.office_server.external_id
#   wait_timeout = "20m"
#   depends_on   = [hcloud_server.office]
# }

# ---------------------------------------------------------------- 3. who may use what

data "awacino_device" "office" {
  for_each = toset(var.office_devices)
  name     = each.value
}

resource "awacino_access_grant" "employee" {
  for_each = {
    for pair in setproduct(keys(var.employees), var.office_devices) : "${pair[0]}/${pair[1]}" => {
      member = pair[0]
      device = pair[1]
    }
  }

  member_id = awacino_member.employee[each.value.member].id
  device_id = data.awacino_device.office[each.value.device].id
  reach     = { kind = "network" }
}

# ---------------------------------------------------------------- 4. the IT partner

data "awacino_device" "it_partner" {
  name = var.it_partner_device
}

resource "awacino_beamin_code" "it_partner" {
  device_id   = data.awacino_device.it_partner.id
  label       = "IT partner - ${var.office}"
  external_id = "it-partner-${lower(var.office)}"
  reach       = { kind = "ports", ports = [22, 443] }
  internet    = "none"
  valid_days  = 30
}

# ---------------------------------------------------------------- outputs

output "join_urls" {
  description = "Invitation links (Awacino also mailed them). Sensitive - keep your state encrypted."
  value       = { for k, m in awacino_member.employee : m.email => m.join_url }
  sensitive   = true
}

output "office_server_cloud_init" {
  description = "cloud-init document for the office server (contains the one-time setup code)."
  value       = awacino_beamin_device.office_server.cloud_init
  sensitive   = true
}

output "it_partner_connect_url" {
  description = "Send this to the IT partner."
  value       = awacino_beamin_code.it_partner.connect_url
  sensitive   = true
}
