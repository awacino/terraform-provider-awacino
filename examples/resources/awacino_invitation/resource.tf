resource "awacino_member" "anna" {
  email = "anna@it-partner.example"
  role  = "read"
}

# A fresh invitation code for a member who has not joined yet (revokes the older one).
# Change `round` to issue another one.
resource "awacino_invitation" "anna" {
  member_id = awacino_member.anna.id
  days      = 3
  send_mail = true
  locale    = "en"

  triggers = {
    round = "2"
  }
}
