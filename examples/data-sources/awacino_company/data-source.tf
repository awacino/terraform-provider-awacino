data "awacino_company" "this" {}

output "free_seats" {
  value = data.awacino_company.this.seats_total - data.awacino_company.this.seats_used
}

# Use only the reach values the server enforces today.
output "can_restrict_to_one_device" {
  value = contains(data.awacino_company.this.access_capabilities, "device")
}
