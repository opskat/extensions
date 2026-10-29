package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescriptorIsValid(t *testing.T) {
	require.NoError(t, descriptor().Validate())
}

// The grid edits, copy and export as SQL are offered exactly because the dialect
// writes rows: the host hides them, and the session refuses them, when undeclared.
// ROWID finds a row of a table without a primary key, so the grid shows no
// "no primary key" warning.
func TestDescriptorDeclaresRowWriting(t *testing.T) {
	d := descriptor()
	_, writes := any(dialect{}).(sqldb.RowWriter)
	_, edits := any(dialect{}).(sqldb.RowEditor)
	require.True(t, writes && edits)
	assert.Equal(t, writes, d.Supports(dbdriver.CapExportSQL))
	assert.Equal(t, writes, d.Supports(dbdriver.CapCopySQL))
	assert.Equal(t, writes && edits, d.Supports(dbdriver.CapGridEdit))
	_, exact := dialect{}.ExactRow(`"T"`, "1 = 1")
	assert.True(t, exact)
	assert.Equal(t, exact, d.LocatesRowWithoutPrimaryKey)
}

func TestDriverOptionsRequireServiceName(t *testing.T) {
	_, err := descriptor().CompleteDriverOptions(map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"service_name"`)

	_, err = descriptor().CompleteDriverOptions(map[string]any{"service_name": ""})
	require.Error(t, err)
}

func TestDriverOptionsDefaults(t *testing.T) {
	opts, err := descriptor().CompleteDriverOptions(map[string]any{"service_name": "ORCLPDB1"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"service_name": "ORCLPDB1", "use_sid": false, "role": "NORMAL"}, opts)
}

func TestDriverOptionsRole(t *testing.T) {
	d := descriptor()
	for _, role := range []string{"NORMAL", "SYSDBA", "SYSOPER"} {
		assert.NoError(t, d.CheckDriverOptions(map[string]any{"role": role}), role)
	}
	assert.Error(t, d.CheckDriverOptions(map[string]any{"role": "SYSASM"}))
	assert.Error(t, d.CheckDriverOptions(map[string]any{"use_sid": "yes"}))
}

// The host refuses to load the extension when the manifest's drivers differ from
// the registered ones, and refuses a Kerberos field group without the grant. The
// manifest is read with the host's own strict parser, so a field the host does
// not know fails here too.
func TestManifestGrantsWhatTheDriverUses(t *testing.T) {
	data, err := os.ReadFile("manifest.json")
	require.NoError(t, err)
	m, err := extmanifest.Parse(data)
	require.NoError(t, err)

	d := descriptor()
	assert.Equal(t, []string{string(d.ID)}, m.Contributes.DatabaseDrivers)
	assert.Equal(t, d.UsesField(dbdriver.FieldKerberos), m.Permissions.Kerberos)
	// The driver dials the asset's own endpoint, and go-ora follows listener
	// redirects (shared server dispatchers, SCAN / RAC local listeners), each
	// dial going through the asset's route.
	assert.True(t, m.Permissions.Network.AssetEndpoint)
	assert.True(t, m.Permissions.Network.AssetRoute)
}

// Driver field labels and descriptions are locale keys the host resolves with the
// extension's translations; a missing key would show the raw key.
func TestDriverFieldLabelsAreTranslated(t *testing.T) {
	for _, file := range []string{"locales/en.json", "locales/zh-CN.json"} {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		var messages map[string]string
		require.NoError(t, json.Unmarshal(data, &messages), file)
		for _, f := range descriptor().DriverFields {
			for _, key := range []string{f.Label, f.Description} {
				require.NotEmpty(t, key, "driver field %q", f.Key)
				assert.NotEmpty(t, messages[key], "%s: %s", file, key)
			}
		}
	}
}

// Creating, dropping, truncating and altering tables are declared because the
// session generates them (CREATE / DROP shared, TRUNCATE and ALTER by the dialect).
func TestDescriptorDeclaresTableDDL(t *testing.T) {
	d := descriptor()
	_, truncates := any(dialect{}).(sqldb.TableTruncator)
	_, alters := any(dialect{}).(sqldb.TableAlterer)
	require.True(t, truncates && alters)
	for _, c := range []dbdriver.Capability{dbdriver.CapCreateTable, dbdriver.CapDropTable} {
		assert.True(t, d.Supports(c), "capability %q", c)
	}
	assert.Equal(t, truncates, d.Supports(dbdriver.CapTruncateTable))
	assert.Equal(t, alters, d.Supports(dbdriver.CapAlterTable))
}

// Import is declared because the dialect writes the statements; Oracle has no
// foreign-key switch, so the import offers no "ignore foreign keys".
func TestDescriptorDeclaresImport(t *testing.T) {
	d := descriptor()
	_, imports := any(dialect{}).(sqldb.Importer)
	_, togglesForeignKeys := any(dialect{}).(sqldb.ForeignKeyChecks)
	require.True(t, imports)
	assert.Equal(t, imports, d.Supports(dbdriver.CapImport))
	assert.False(t, togglesForeignKeys)
	assert.False(t, d.CanDisableForeignKeyChecks)
}
