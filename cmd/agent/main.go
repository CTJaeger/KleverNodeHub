package main

import (
	"fmt"

	"github.com/CTJaeger/KleverNodeHub/internal/version"
)

func main() {
	info := version.Get()
	fmt.Printf("Klever Node Hub - Agent %s (%s)\n", info.Version, info.GitCommit)
}
