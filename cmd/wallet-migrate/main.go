// Command wallet-migrate loads a BudgetBakers Wallet CSV export into a reset
// EUR-base Wallet through the Wallet REST API, for the USD->EUR main-currency
// migration. See internal/app/walletmigrate for the flags and behaviour.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dpcat237/bbwalet-utils/internal/app/walletmigrate"
)

// version is set at build time via -ldflags '-X main.version=...'.
var version = "dev"

func main() {
	if err := walletmigrate.Run(context.Background(), version, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "wallet-migrate:", err)
		os.Exit(1)
	}
}
