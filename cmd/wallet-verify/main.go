// Command wallet-verify compares an archived BudgetBakers Wallet export against
// the loaded Wallet state via the REST API and reports per-account count/sum
// agreement, a main-currency and historical-rate diagnostic, and a category
// spot-check. It is read-only. See internal/app/walletverify for the flags.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dpcat237/bbwalet-utils/internal/app/walletverify"
)

// version is set at build time via -ldflags '-X main.version=...'.
var version = "dev"

func main() {
	if err := walletverify.Run(context.Background(), version, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "wallet-verify:", err)
		os.Exit(1)
	}
}
