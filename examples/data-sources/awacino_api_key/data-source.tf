data "awacino_api_key" "this" {}

# Warn a pipeline 14 days before its key ends.
check "api_key_lifetime" {
  assert {
    condition     = data.awacino_api_key.this.expires_at == null ? true : timecmp(data.awacino_api_key.this.expires_at, timeadd(plantimestamp(), "336h")) > 0
    error_message = "The Awacino API key ends within 14 days - create a new one in the app (Company settings > Awacino API)."
  }
}
