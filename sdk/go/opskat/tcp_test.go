//go:build !wasip1

package opskat

import (
	"context"
	"net"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTCPDialBasic(t *testing.T) {
	Convey("Given a TestHost with a mock TCP echo server", t, func() {
		th := NewTestHost(WithMockTCP(func(addr string) (net.Conn, error) {
			a, b := net.Pipe()
			go func() {
				buf := make([]byte, 256)
				n, _ := a.Read(buf)
				a.Write(buf[:n]) //nolint:errcheck // echo pipe
			}()
			return b, nil
		}))
		defer th.Close()

		Convey("DialContext returns a working net.Conn", func() {
			conn, err := DialContext(context.Background(), "tcp", "broker:9092")
			So(err, ShouldBeNil)
			defer conn.Close()

			n, err := conn.Write([]byte("hi"))
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)

			buf := make([]byte, 8)
			n, err = conn.Read(buf)
			So(err, ShouldBeNil)
			So(string(buf[:n]), ShouldEqual, "hi")
		})
	})
}
