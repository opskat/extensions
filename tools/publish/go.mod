module github.com/opskat/extensions/tools/publish

go 1.26.0

// Development only: points at the opskat checkout that carries pkg/extstore.
// Pin to the released opskat version (and drop this line) once it is merged.
replace github.com/opskat/opskat => /Users/codfrm/Code/opskat/opskat/.dev-kit/worktrees/2026-10-04-ext-store

require (
	github.com/opskat/opskat v0.0.0-00010101000000-000000000000
	go.uber.org/zap v1.28.0
)

require (
	github.com/cago-frame/cago v0.0.0-20260609091633-ba2f550b2729 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
