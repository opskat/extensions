package main

import "github.com/opskat/opskat/pkg/dbdriver"

// driverID is the id stored in the asset config's driver field; renaming it is a
// data migration.
const driverID dbdriver.DriverID = "oracle"

// Keys of the driver fields: the values arrive as OpenRequest.DriverOptions, and
// opsctl / the AI fill them under these keys in driver_options.
const (
	// optServiceName is the service name, or the SID when optUseSID is on. It is a
	// driver field rather than the form's database field because the host swaps
	// Config.Database for the schema a tree node, tab or scope picks.
	optServiceName = "service_name"
	optUseSID      = "use_sid"
	optRole        = "role"
)

// Values of optRole: the administrative privilege the session logs in with.
const (
	roleNormal  = "NORMAL"
	roleSYSDBA  = "SYSDBA"
	roleSYSOPER = "SYSOPER"
)

// descriptor describes the Oracle driver. There is no read-only connection
// (Oracle has no read-only session and go-ora no read-only transaction), no
// foreign-key switch for imports (CanDisableForeignKeyChecks stays false) and no
// "new database" (creating an Oracle database is not a user's operation).
func descriptor() dbdriver.Descriptor {
	return dbdriver.Descriptor{
		ID:          driverID,
		Name:        "Oracle",
		Order:       50,
		Aliases:     []string{},
		DefaultPort: 1521,
		Form: []dbdriver.FormTabFields{
			{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
				dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldKerberos,
			}},
			{Tab: dbdriver.TabTunnel, Fields: []dbdriver.FormField{dbdriver.FieldTunnel}},
			{Tab: dbdriver.TabTLS, Fields: []dbdriver.FormField{dbdriver.FieldTLSCert}},
			{Tab: dbdriver.TabAdvanced, Fields: []dbdriver.FormField{dbdriver.FieldQueryTimeout}},
		},
		Capabilities: []dbdriver.Capability{
			dbdriver.CapClassifyStatements,
			dbdriver.CapViewDDL, dbdriver.CapViews, dbdriver.CapProcedures, dbdriver.CapFunctions, dbdriver.CapTriggers,
			dbdriver.CapGridEdit, dbdriver.CapImport, dbdriver.CapExportSQL, dbdriver.CapCopySQL,
			dbdriver.CapCreateTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable, dbdriver.CapAlterTable,
		},
		// ROWID finds the one row to change or delete in a table without a
		// primary key (write.go).
		LocatesRowWithoutPrimaryKey: true,
		AlterTableAttributes:        alterAttributes,
		DriverFields: []dbdriver.DriverField{
			{
				Key: optServiceName, Type: dbdriver.FieldTypeText, Tab: dbdriver.TabConnection, Required: true,
				Label: "driver.serviceName.label", Description: "driver.serviceName.desc",
			},
			{
				Key: optUseSID, Type: dbdriver.FieldTypeSwitch, Tab: dbdriver.TabConnection, Default: false,
				Label: "driver.useSID.label", Description: "driver.useSID.desc",
			},
			{
				Key: optRole, Type: dbdriver.FieldTypeEnum, Tab: dbdriver.TabConnection, Default: roleNormal,
				Enum:  []string{roleNormal, roleSYSDBA, roleSYSOPER},
				Label: "driver.role.label", Description: "driver.role.desc",
			},
		},
	}
}
