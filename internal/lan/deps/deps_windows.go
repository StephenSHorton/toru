// Package deps pins the LAN feature's dependencies in go.mod while the
// packages that use them are being written. Nothing imports it; delete it
// once internal/lan and cmd/toru-lan-* import these directly.
package deps

import (
	_ "github.com/Microsoft/go-winio"
	_ "github.com/pion/ice/v4"
	_ "github.com/pion/turn/v5"
	_ "golang.zx2c4.com/wireguard/device"
	_ "golang.zx2c4.com/wireguard/tun"
	_ "golang.zx2c4.com/wireguard/tun/netstack"
	_ "golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)
