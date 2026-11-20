// Command terraform-provider-awacino is the official Terraform provider for the Awacino API.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/awacino/terraform-provider-awacino/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// Generate the Registry docs (docs/) from the schema, examples/ and templates/.
//go:generate go tool tfplugindocs generate --provider-name awacino

// version is set by GoReleaser (-X main.version=...).
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/awacino/awacino",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
