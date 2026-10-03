// Command goatlassian groups git repositories, kata trackers, owcli wikis,
// and coding-agent sessions into projects and reports on the portfolio.
package main

import (
	"os"

	"github.com/Hoodoo/goatlassian/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
