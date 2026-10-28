// Command api is the analytics-service process entry point. It does one
// thing: hand control to lib/boot. All wiring lives there.
package main

import (
	"os"

	"github.com/quickbite/analytics-service/lib/boot"
)

func main() {
	if err := boot.Run(); err != nil {
		os.Exit(1)
	}
}
