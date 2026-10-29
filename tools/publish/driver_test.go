package main

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"
)

// A hostABI 1.0 database driver package is one the publisher can describe, and
// its grants are what the index lists.
func TestDescribePackageAcceptsHostABI10DatabaseDriver(t *testing.T) {
	ctx := context.Background()
	root := newRepo(t, nil)
	addDriverExtension(t, root, "sampledriver")
	b := &fakeBuilder{wasm: map[string]string{"sampledriver": driverWasm}}
	dist, err := b.Build(ctx, filepath.Join(root, "extensions", "sampledriver"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(t.TempDir(), "sampledriver.zip")
	if err := zipDir(dist, pkg); err != nil {
		t.Fatal(err)
	}

	got, err := describePackage(ctx, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.HostABI != "1.0" {
		t.Errorf("hostABI = %s, want 1.0", got.Manifest.HostABI)
	}
	want := extmanifest.Permissions{Kerberos: true, Network: extmanifest.NetworkPermission{AssetEndpoint: true, AssetRoute: true}}
	if !reflect.DeepEqual(got.Manifest.Permissions, want) {
		t.Errorf("permissions = %+v, want %+v", got.Manifest.Permissions, want)
	}
	if !slices.Equal(got.Manifest.Contributes.DatabaseDrivers, []string{"sampledb"}) {
		t.Errorf("databaseDrivers = %v", got.Manifest.Contributes.DatabaseDrivers)
	}
}

// Publishing a database driver lists permissions and databaseDrivers in its index entry and leaves an entry published earlier
// (decoded) as it was.
func TestPublishListsDatabaseDriverGrantsAndKeepsOtherEntries(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	if _, err := newPublisher(root, priv, &fakeBuilder{}, &fakePusher{}, testClock).publish(ctx); err != nil {
		t.Fatal(err)
	}
	before, _, _ := readSignedIndex(t, root, pub)

	addDriverExtension(t, root, "sampledriver")
	b := &fakeBuilder{wasm: map[string]string{"sampledriver": driverWasm}}
	published, err := newPublisher(root, priv, b, &fakePusher{}, testClock).publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(published, []string{"sampledriver:0.1.0"}) {
		t.Fatalf("published = %v", published)
	}

	after, _, _ := readSignedIndex(t, root, pub)
	if len(after.Extensions) != 2 {
		t.Fatalf("index extensions = %+v", after.Extensions)
	}
	if !reflect.DeepEqual(findExtension(&after, "sample"), findExtension(&before, "sample")) {
		t.Errorf("sample entry changed:\n%+v\nwas %+v", findExtension(&after, "sample"), findExtension(&before, "sample"))
	}
	driver := findExtension(&after, "sampledriver")
	if driver == nil || len(driver.Versions) != 1 {
		t.Fatalf("driver entry = %+v", driver)
	}
	v := driver.Versions[0]
	if v.HostABI != "1.0" {
		t.Errorf("hostABI = %s, want 1.0", v.HostABI)
	}
	if !slices.Equal(v.Contributes.DatabaseDrivers, []string{"sampledb"}) || !v.Permissions.Kerberos ||
		!v.Permissions.Network.AssetEndpoint || !v.Permissions.Network.AssetRoute {
		t.Errorf("permissions = %+v, contributes = %+v", v.Permissions, v.Contributes)
	}
}
