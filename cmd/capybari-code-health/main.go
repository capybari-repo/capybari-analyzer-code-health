// Command capybari-code-health runs this capability on its own.
package main

import (
	codehealth "github.com/capybari/capybari-analyzer-code-health"
	"github.com/capybari/capybari-core/standalone"
)

var version = "dev"

func main() { standalone.Main(version, codehealth.New()) }
