// Command oracle is the OpsKat extension that provides the Oracle Database driver
// (Oracle 19c and later) for SQL database assets, built on go-ora. Like every
// reactor guest it registers from init(); func main() is never called.
package main

import (
	"os"
	"runtime/debug"

	opskat "github.com/opskat/opskat/pkg/extsdk"
	"github.com/opskat/opskat/pkg/extsdk/sdkdbdriver"
)

func main() {}

func init() {
	// A reactor module has no argv; go-ora reads os.Args[0] as the program name
	// it reports to the database.
	if len(os.Args) == 0 {
		os.Args = []string{"opskat"}
	}
	// The instance's memory is capped at 512 MiB (the host's instanceMemoryPages), the
	// module's 20 MiB included, never shrinks, and a large LOB value is held whole
	// together with what is made of it (values.go, maxLobBytes; write.go): past a
	// small part of it (64 MiB), collect as
	// often as it takes rather than let garbage grow the memory toward the cap
	// (measured against the former 256 MiB cap: a 16 MB BLOB's row statement peaked
	// at 248 MiB with Go's default pacing, 203 MiB with this limit).
	debug.SetMemoryLimit(64 << 20)
	opskat.Extension(opskat.Meta{
		Icon:        "oracle",
		DisplayName: "extension.displayName",
		Description: "extension.description",
	})
	sdkdbdriver.DatabaseDriver(descriptor(), open, sdkdbdriver.WithStatementClassifier(splitOracle))
}
