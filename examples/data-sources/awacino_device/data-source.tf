data "awacino_device" "nas" {
  name = "Office Hamburg - NAS"
}

# Wait for a server that registers during this apply (see awacino_beamin_device).
# data "awacino_device" "web01" {
#   external_id  = awacino_beamin_device.web01.external_id
#   wait_timeout = "15m"
#   depends_on   = [hcloud_server.web01]
# }
