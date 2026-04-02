module github.com/opskat/extensions/examples/echo

go 1.25

require (
	github.com/opskat/extensions/sdk/go/opskat v0.0.0
	github.com/smartystreets/goconvey v1.8.1
)

require (
	github.com/gopherjs/gopherjs v1.17.2 // indirect
	github.com/jtolds/gls v4.20.0+incompatible // indirect
	github.com/smarty/assertions v1.15.0 // indirect
)

replace github.com/opskat/extensions/sdk/go/opskat => ../../../sdk/go/opskat
