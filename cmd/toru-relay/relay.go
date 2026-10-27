package main

import (
	"errors"
	"net"
	"time"

	"github.com/pion/turn/v5"
)

// relay.go assembles the pion/turn server from listeners the caller opened.
// main opens them on the public interface; tests open them on 127.0.0.1.

// config is everything the relay needs, from flags or environment.
type config struct {
	Secret     string
	Realm      string
	PublicIP   net.IP // relay address handed to clients (XOR-RELAYED-ADDRESS)
	PublicHost string // name in the URIs; defaults to PublicIP
	ListenIP   string // bind address for every socket

	UDPPort, TCPPort, TLSPort  int
	RelayMinPort, RelayMaxPort int
	CertFile, KeyFile          string
	HTTPSAddr, HTTPAddr        string // credential endpoint (HTTPS with the cert, or plain behind a proxy)
	TrustForward               bool

	CredTTL       time.Duration
	AllocLifetime time.Duration
	UserKbps      int // per-user relay bandwidth, both directions together; 0 = unlimited
	IPKbps        int // per-client-IP relay bandwidth (all its users); 0 = unlimited
	MaxPerUser    int
	MaxPerIP      int
	MaxAllocs     int
	CredPerMin    int
	CredBurst     int
}

const minSecretLen = 16

var errShortSecret = errors.New("secret must be at least 16 bytes (try: openssl rand -base64 32)")

func (c *config) validate() error {
	if len(c.Secret) < minSecretLen {
		return errShortSecret
	}
	if c.PublicIP == nil {
		return errors.New("public IP is required (-public-ip)")
	}
	if c.RelayMinPort < 1 || c.RelayMaxPort > 65535 || c.RelayMinPort > c.RelayMaxPort {
		return errors.New("relay port range is invalid")
	}
	if c.AllocLifetime <= 0 || c.AllocLifetime > 10*time.Minute {
		return errors.New("allocation lifetime must be between 1s and 10m")
	}
	if c.CredTTL <= 0 {
		return errors.New("credential ttl must be positive")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return errors.New("-cert and -key go together")
	}
	return nil
}

// newRelayGen builds the generator shared by every listener.
func newRelayGen(c *config, own *ownAddrs) *relayGen {
	rate, burst := bucketFor(c.UserKbps)
	ipRate, ipBurst := bucketFor(c.IPKbps)
	return &relayGen{
		inner: &turn.RelayAddressGeneratorPortRange{
			RelayAddress: c.PublicIP,
			Address:      c.ListenIP,
			MinPort:      uint16(c.RelayMinPort), //nolint:gosec // validated 1..65535
			MaxPort:      uint16(c.RelayMaxPort), //nolint:gosec // validated 1..65535
		},
		own:        own,
		userRate:   rate,
		userBurst:  burst,
		ipRate:     ipRate,
		ipBurst:    ipBurst,
		maxPerUser: c.MaxPerUser,
		maxPerIP:   c.MaxPerIP,
		maxAll:     c.MaxAllocs,
		users:      map[string]*userState{},
		ips:        map[string]*userState{},
		now:        time.Now,
	}
}

// bucketFor turns a kbit/s cap into a token bucket's rate and burst.
func bucketFor(kbps int) (rate, burst float64) {
	rate = float64(kbps) * 1000 / 8
	burst = rate // one second's worth...
	if burst < 64*1024 {
		burst = 64 * 1024 // ...but never below a few full-size packets' worth of slack
	}
	return rate, burst
}

// newServer starts pion/turn on the given sockets. permit decides peer IPs
// (allowedPeer in production).
func newServer(c *config, gen *relayGen, permit turn.PermissionHandler,
	udp []net.PacketConn, listeners []net.Listener) (*turn.Server, error) {
	sc := turn.ServerConfig{
		Realm:       c.Realm,
		AuthHandler: authHandler(c.Secret),
		// The server's default and what it answers when the client asks for
		// nothing. pion honours a client-requested LIFETIME below one hour,
		// so this is not a hard cap. An allocation lasts at most one lifetime
		// past its credential's expiry (every Refresh needs a valid one);
		// the allocation caps per user, per client IP and in total bound how
		// many there are (see README).
		AllocationLifetime: c.AllocLifetime,
	}
	for _, pc := range udp {
		sc.PacketConnConfigs = append(sc.PacketConnConfigs, turn.PacketConnConfig{
			PacketConn: pc, RelayAddressGenerator: gen, PermissionHandler: permit,
		})
	}
	for _, l := range listeners {
		sc.ListenerConfigs = append(sc.ListenerConfigs, turn.ListenerConfig{
			Listener: l, RelayAddressGenerator: gen, PermissionHandler: permit,
		})
	}
	return turn.NewServer(sc)
}

// permitPeers adapts allowedPeer to pion's PermissionHandler.
func permitPeers(_ net.Addr, peer net.IP) bool { return allowedPeer(peer) }
