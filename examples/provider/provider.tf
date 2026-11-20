terraform {
  required_providers {
    awacino = {
      source  = "awacino/awacino"
      version = "~> 1.0"
    }
  }
}

# The key comes from the environment: AWACINO_KEY_ID and AWACINO_KEY_SECRET.
# Create it in the Awacino app: Company settings > Awacino API > New API key,
# with the sections Users and Connections (Read and Write).
provider "awacino" {}

# Or explicitly, for example from a secret store:
# provider "awacino" {
#   key_id     = var.awacino_key_id
#   key_secret = var.awacino_key_secret        # sensitive
#   endpoint   = "https://api.awacino.net"     # default; AWACINO_ENDPOINT overrides it
# }
