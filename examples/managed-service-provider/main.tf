# A managed service provider: a technician with access to two customer devices until the contract ends.

terraform {
  required_version = ">= 1.5"
  required_providers {
    awacino = { source = "awacino/awacino", version = "~> 0.1" }
  }
}

provider "awacino" {}

data "awacino_device" "nas" {
  name = "Customer 12 - NAS"
}

data "awacino_device" "server" {
  name = "Customer 12 - Server"
}

resource "awacino_member" "tech_anna" {
  email        = "anna@it-partner.example"
  display_name = "Anna Berger"
  external_id  = "tech-017"
  role         = "read"

  send_invitation = {
    days   = 7
    locale = "de"
  }
}

resource "awacino_access_grant" "anna" {
  for_each = {
    nas    = data.awacino_device.nas.id
    server = data.awacino_device.server.id
  }

  member_id   = awacino_member.tech_anna.id
  device_id   = each.value
  reach       = { kind = "network" }
  valid_until = "2027-06-30T00:00:00Z" # contract end
}
