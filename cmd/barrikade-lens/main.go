package main

import (
	"os"

	"github.com/barrikadelabs/barrikade-lens/internal/cli"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/endpoint"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/repository"
)

var version = "2.0.0-dev"

func main() {
	cli.Version, endpoint.Version, repository.Version = version, version, version
	os.Exit(cli.Execute())
}
