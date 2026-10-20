package main

import (
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/examples/cinema/services/checkout"
)

func main() { cinema.Run("cinema-checkout", checkout.Register, nil) }
