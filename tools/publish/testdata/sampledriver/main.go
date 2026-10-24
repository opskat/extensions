// Command sampledriver is the hostABI 2.5 database driver extension the
// publisher's tests package: a real WASM guest whose describe() declares one SQL
// driver, so the grants the index lists come through the path the app takes. It
// is never opened — packaging only runs describe().
package main

import (
	"context"
	"database/sql"
	"errors"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	opskat "github.com/opskat/opskat/pkg/extsdk"
	"github.com/opskat/opskat/pkg/extsdk/sdkdbdriver"
)

func main() {}

var descriptor = dbdriver.Descriptor{
	ID:          "sampledb",
	Name:        "SampleDB",
	Order:       900,
	Aliases:     []string{},
	DefaultPort: 7357,
	Form: []dbdriver.FormTabFields{
		{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
			dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldKerberos,
		}},
	},
	Capabilities: []dbdriver.Capability{},
}

func init() {
	opskat.Extension(opskat.Meta{Icon: "database", DisplayName: "extension.displayName", Description: "extension.description"})
	sdkdbdriver.DatabaseDriver(descriptor, open)
}

func open(context.Context, dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) {
	return nil, nil, errors.New("sampledriver does not connect")
}
