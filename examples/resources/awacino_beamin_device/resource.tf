# A one-time setup code for a new server, with ready install lines.
resource "awacino_beamin_device" "web01" {
  label       = "web-01.example.net"
  external_id = "cust4711-web01"
  hours       = 2
}

# Put the setup code into the server's user data - here Hetzner Cloud as an example:
# resource "hcloud_server" "web01" {
#   name        = "web-01"
#   server_type = "cx22"
#   image       = "debian-12"
#   user_data   = awacino_beamin_device.web01.cloud_init
# }

# The registered device, in the same apply: the data source waits until the server has registered.
# (Do not set wait_for_device on web01 here - the server is only created after web01 is done.)
# data "awacino_device" "web01" {
#   external_id  = awacino_beamin_device.web01.external_id
#   wait_timeout = "15m"
#   depends_on   = [hcloud_server.web01]
# }

# A server that is set up outside this configuration: wait in the resource itself.
resource "awacino_beamin_device" "db01" {
  label           = "db-01.example.net"
  wait_for_device = true
  wait_timeout    = "30m"
}

output "db01_install" {
  value     = awacino_beamin_device.db01.install_apt
  sensitive = true
}
