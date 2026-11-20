data "awacino_device" "web01" {
  name = "web-01"
}

data "awacino_active_connections" "web01" {
  device_id = data.awacino_device.web01.id
}

output "connected_now" {
  value = [for c in data.awacino_active_connections.web01.connections : "${c.via}: ${c.peer_name} since ${c.since}"]
}
