data "awacino_devices" "servers" {
  role = "beamin"
  kind = "unattended"
}

output "servers_in_lobby" {
  value = [for d in data.awacino_devices.servers.devices : d.name if d.status == "lobby"]
}
