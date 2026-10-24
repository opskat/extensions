module github.com/opskat/extensions/oracle

go 1.26.0

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/opskat/opskat v1.14.1-0.20261023101603-dbb46b7a9133
	github.com/sijms/go-ora/v2 v2.9.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/cago-frame/cago v0.0.0-20260926025905-604723c9e69d // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// go-ora v2.9.0 with the two patches it needs to build for GOOS=wasip1 (no
// out-of-band break, no host IP lookup); github.com/opskat/go-ora, branch opskat-wasip1.
replace github.com/sijms/go-ora/v2 => github.com/opskat/go-ora/v2 v2.9.1-opskat.1
