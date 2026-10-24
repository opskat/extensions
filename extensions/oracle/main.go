// Command oracle is the OpsKat extension that provides the Oracle Database driver
// (Oracle 19c and later) for SQL database assets, built on go-ora. Like every
// reactor guest it registers from init(); func main() is never called.
package main

import (
	"os"

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
	opskat.Extension(opskat.Meta{
		Icon:        "oracle",
		DisplayName: "extension.displayName",
		Description: "extension.description",
	})
	sdkdbdriver.DatabaseDriver(descriptor(), open, sdkdbdriver.WithStatementClassifier(splitOracle))
}
