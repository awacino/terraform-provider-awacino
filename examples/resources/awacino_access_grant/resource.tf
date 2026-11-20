data "awacino_device" "nas" {
  name = "Customer 12 - NAS"
}

resource "awacino_member" "tech_anna" {
  email       = "anna@it-partner.example"
  external_id = "tech-017"
  role        = "read" # grants restrict members with role read
}

# Anna reaches the NAS and its network until the contract ends.
resource "awacino_access_grant" "anna_nas" {
  member_id   = awacino_member.tech_anna.id
  device_id   = data.awacino_device.nas.id
  reach       = { kind = "network" }
  valid_until = "2027-06-30T00:00:00Z"
}
