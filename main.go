// Command terraform-provider-lambdabuild serves the lambdabuild Terraform provider.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/cloudandthings/terraform-provider-lambdabuild/internal/provider"
)

// version is set by goreleaser via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: provider.Address,
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
