package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extmanifest"
)

// manifest.json 必须能被宿主严格解析，并声明审核器与设置端点：describe 里的 commandReviewer 与
// settings 要和它一致，否则加载时被宿主拒绝。
func TestManifestDeclaresReviewerAndSettingsEndpoint(t *testing.T) {
	raw, err := os.ReadFile("manifest.json")
	require.NoError(t, err)
	m, err := extmanifest.Parse(raw)
	require.NoError(t, err)

	assert.Equal(t, "jev-command-review", m.Name)
	assert.Equal(t, "0.1.0", m.Version)
	assert.Equal(t, "1.15.0", m.MinAppVersion)
	assert.Equal(t, "1.0", m.HostABI)
	assert.True(t, m.Permissions.Network.SettingsEndpoint)
	assert.True(t, m.Contributes.CommandReviewer)
}
