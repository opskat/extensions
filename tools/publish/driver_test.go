package main

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/opskat/opskat/pkg/extension"
)

// A hostABI 2.5 database driver package is one the publisher can describe, and
// its grants are what the index lists.
func TestDescribePackageAcceptsHostABI25DatabaseDriver(t *testing.T) {
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
	if got.Manifest.HostABI != "2.5" {
		t.Errorf("hostABI = %s, want 2.5", got.Manifest.HostABI)
	}
	want := extension.Capabilities{
		DatabaseDrivers:  []string{"sampledb"},
		DatabaseKerberos: true,
		Network:          extension.NetworkCapability{DatabaseRedirects: true},
	}
	if !reflect.DeepEqual(got.Manifest.Capabilities, want) {
		t.Errorf("capabilities = %+v, want %+v", got.Manifest.Capabilities, want)
	}
}

// Publishing a database driver lists databaseDrivers / databaseKerberos /
// databaseRedirects in its index entry and leaves an entry published earlier
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
	if v.HostABI != "2.5" {
		t.Errorf("hostABI = %s, want 2.5", v.HostABI)
	}
	if !slices.Equal(v.Capabilities.DatabaseDrivers, []string{"sampledb"}) || !v.Capabilities.DatabaseKerberos || !v.Capabilities.Network.DatabaseRedirects {
		t.Errorf("capabilities = %+v", v.Capabilities)
	}
}
