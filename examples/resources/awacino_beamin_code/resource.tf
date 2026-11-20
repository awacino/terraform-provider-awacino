data "awacino_device" "web01" {
  name = "web-01"
}

# A Beam-in code for a customer: only remote login and HTTPS on this server, 30 days.
resource "awacino_beamin_code" "customer" {
  device_id   = data.awacino_device.web01.id
  label       = "Customer 4711 - web-01"
  external_id = "cust4711-web01-code"
  reach       = { kind = "ports", ports = [22, 443] }
  internet    = "none"
  valid_days  = 30
}

# Hand this to the customer (it is only known right after the code was created).
output "customer_connect_url" {
  value     = awacino_beamin_code.customer.connect_url
  sensitive = true
}
