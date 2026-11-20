data "awacino_members" "provisioned" {
  role   = "read"
  status = "active"
}

output "provisioned_emails" {
  value = [for m in data.awacino_members.provisioned.members : m.email]
}
