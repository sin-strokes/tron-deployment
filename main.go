package main

import (
	"os"

	"github.com/tronprotocol/tron-deployment/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
