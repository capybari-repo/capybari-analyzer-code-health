// Command capybari-code-health runs this capability on its own.
package main

import (
	codehealth "github.com/capybari-repo/capybari-analyzer-code-health"
	"github.com/capybari-repo/capybari-core/standalone"
)

var version = "dev"

func main() { standalone.Main(version, codehealth.New()) }
