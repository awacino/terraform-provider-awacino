# A hoster: a new customer server and a Beam-in code for its customer, in one apply.
#   awacino_beamin_device (setup code) -> the cloud server boots with cloud_init -> the data source waits until it
#   registered -> awacino_beamin_code for that device (remote login + HTTPS, 30 days) -> connect_url to the customer.

terraform {
  required_version = ">= 1.5"
  required_providers {
    awacino = { source = "awacino/awacino", version = "~> 1.0" }
    hcloud  = { source = "hetznercloud/hcloud", version = "~> 1.48" }
  }
}

provider "awacino" {}
provider "hcloud" {}

variable "customer" {
  type    = string
  default = "Customer 4711"
}

variable "customer_id" {
  type    = string
  default = "cust4711"
}

# 1. A one-time setup code for the new server, with a ready cloud-init document.
resource "awacino_beamin_device" "web01" {
  label       = "web-01.${var.customer_id}.example.net"
  external_id = "${var.customer_id}-web01"
  hours       = 2
}

# 2. The server itself. The setup code travels in user_data; the client connects outbound only.
resource "hcloud_server" "web01" {
  name        = "${var.customer_id}-web-01"
  server_type = "cx22"
  image       = "debian-12"
  user_data   = awacino_beamin_device.web01.cloud_init
}

# 3. Wait until the server has registered as a Beam-in device.
data "awacino_device" "web01" {
  external_id  = awacino_beamin_device.web01.external_id
  wait_timeout = "15m"
  depends_on   = [hcloud_server.web01]
}

# 4. A Beam-in code for the customer: only remote login and HTTPS on this server, 30 days.
resource "awacino_beamin_code" "web01_customer" {
  device_id   = data.awacino_device.web01.id
  label       = "${var.customer} - web-01"
  external_id = "${var.customer_id}-web01-code"
  reach       = { kind = "ports", ports = [22, 443] }
  internet    = "none"
  valid_days  = 30
}

output "customer_connect_url" {
  value     = awacino_beamin_code.web01_customer.connect_url
  sensitive = true
}
