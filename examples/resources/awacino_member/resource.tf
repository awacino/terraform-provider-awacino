# A new employee. role "read" = uses only the devices you grant (awacino_access_grant).
resource "awacino_member" "dieter" {
  email        = "dieter@company.example"
  display_name = "Dieter Muster"
  external_id  = "4711" # employee number - `terraform import awacino_member.dieter external_id:4711`
  role         = "read"

  # Awacino mails the invitation (in German). Without this block, send join_url in your own mail.
  send_invitation = {
    days   = 7
    locale = "de"
  }
}

output "dieter_join_url" {
  value     = awacino_member.dieter.join_url
  sensitive = true
}
