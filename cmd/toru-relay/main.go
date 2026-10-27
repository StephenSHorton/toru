// Command toru-relay is Toru's TURN relay: a pion/turn server for the cases
// where two PCs cannot reach each other directly (both behind symmetric NAT or
// CGNAT, or on a network that blocks UDP), plus the HTTPS endpoint that hands
// out short-lived credentials for it. Call, Watch and LAN all use it.
//
// Relayed traffic is already end-to-end encrypted (DTLS-SRTP for calls and
// streams, WireGuard for LAN), so the relay can neither read nor change it.
// What it must not become is an open proxy: credentials are short-lived TURN
// REST credentials, peers in private and local ranges are refused, and each
// user's bandwidth and allocations are capped.
//
// Design: docs/lan.md, "NAT traversal and relay". Deploying: README.md here.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// version is set at build time with -ldflags "-X main.version=<version>".
var version = "dev"

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	c, err := parseConfig(os.Args[1:], os.LookupEnv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatalf("toru-relay: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		log.Fatalf("toru-relay: %v", err)
	}
}

// envPrefix: every flag can also come from the environment, e.g. -public-ip
// is TORU_RELAY_PUBLIC_IP. A flag on the command line wins.
const envPrefix = "TORU_RELAY_"

func parseConfig(args []string, getenv func(string) (string, bool)) (*config, error) {
	fs := flag.NewFlagSet("toru-relay", flag.ContinueOnError)
	c := &config{}
	var publicIP, secretFile string
	var showVersion bool
	fs.StringVar(&c.Secret, "secret", "", "shared TURN REST secret (prefer TORU_RELAY_SECRET or -secret-file)")
	fs.StringVar(&secretFile, "secret-file", "", "file holding the shared secret")
	fs.StringVar(&c.Realm, "realm", "toru", "TURN realm")
	fs.StringVar(&publicIP, "public-ip", "", "public IPv4 address of this host (required)")
	fs.StringVar(&c.PublicHost, "public-host", "", "public DNS name for the URIs (must match the TLS cert; default: public IP)")
	fs.StringVar(&c.ListenIP, "listen-ip", "0.0.0.0", "address every socket binds to")
	fs.IntVar(&c.UDPPort, "udp-port", 3478, "TURN over UDP (0 = off)")
	fs.IntVar(&c.TCPPort, "tcp-port", 3478, "TURN over TCP (0 = off)")
	fs.IntVar(&c.TLSPort, "tls-port", 443, "TURN over TLS, needs -cert and -key (0 = off)")
	fs.IntVar(&c.RelayMinPort, "relay-min-port", 49152, "lowest relay port")
	fs.IntVar(&c.RelayMaxPort, "relay-max-port", 65535, "highest relay port")
	fs.StringVar(&c.CertFile, "cert", "", "TLS certificate chain (PEM), reloaded when the file changes")
	fs.StringVar(&c.KeyFile, "key", "", "TLS private key (PEM)")
	fs.StringVar(&c.HTTPSAddr, "https-addr", ":8443", "credential endpoint over HTTPS, needs -cert (empty = off)")
	fs.StringVar(&c.HTTPAddr, "http-addr", "", "credential endpoint over plain HTTP, for use behind a TLS reverse proxy (e.g. 127.0.0.1:8080)")
	fs.BoolVar(&c.TrustForward, "trust-forwarded-for", false, "rate-limit on X-Forwarded-For (only behind your own reverse proxy)")
	fs.DurationVar(&c.CredTTL, "cred-ttl", 12*time.Hour, "lifetime of each credential (it bounds a relayed session: see README)")
	fs.DurationVar(&c.AllocLifetime, "alloc-lifetime", 10*time.Minute, "default allocation lifetime, at most 10m")
	fs.IntVar(&c.UserKbps, "user-kbps", 20000, "relay bandwidth per user in kbit/s, both directions together (0 = unlimited)")
	fs.IntVar(&c.MaxPerUser, "max-allocs-per-user", 16, "allocations per user at once (0 = unlimited)")
	fs.IntVar(&c.MaxAllocs, "max-allocs", 2000, "allocations in total at once (0 = unlimited)")
	fs.IntVar(&c.CredPerMin, "cred-rate", 20, "credential requests per minute per client IP (0 = unlimited)")
	fs.IntVar(&c.CredBurst, "cred-burst", 10, "credential requests a client IP may make at once")
	fs.BoolVar(&showVersion, "version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := applyEnv(fs, getenv); err != nil {
		return nil, err
	}
	if showVersion {
		fmt.Println("toru-relay", version)
		return nil, flag.ErrHelp
	}
	if secretFile != "" && c.Secret == "" {
		b, err := os.ReadFile(secretFile)
		if err != nil {
			return nil, fmt.Errorf("read secret file: %w", err)
		}
		c.Secret = strings.TrimSpace(string(b))
	}
	if publicIP != "" {
		if c.PublicIP = net.ParseIP(publicIP).To4(); c.PublicIP == nil {
			return nil, fmt.Errorf("-public-ip %q is not an IPv4 address", publicIP)
		}
	}
	if c.PublicHost == "" && c.PublicIP != nil {
		c.PublicHost = c.PublicIP.String()
	}
	if c.CertFile == "" {
		c.TLSPort = 0 // no cert, no turns:
		c.HTTPSAddr = ""
	}
	return c, c.validate()
}

// applyEnv fills each flag not given on the command line from
// TORU_RELAY_<NAME>, with dashes as underscores.
func applyEnv(fs *flag.FlagSet, getenv func(string) (string, bool)) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var err error
	fs.VisitAll(func(f *flag.Flag) {
		if set[f.Name] || err != nil {
			return
		}
		key := envPrefix + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		if v, ok := getenv(key); ok {
			if e := fs.Set(f.Name, v); e != nil {
				err = fmt.Errorf("%s: %w", key, e)
			}
		}
	})
	return err
}

func run(ctx context.Context, c *config) error {
	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		log.Printf("interface addresses: %v (only -public-ip is treated as our own)", err)
	}
	own := newOwnAddrs(c.PublicIP, ifaceAddrs, c.RelayMinPort, c.RelayMaxPort)
	gen := newRelayGen(c, own)

	var certs *certReloader
	if c.CertFile != "" {
		if certs, err = newCertReloader(c.CertFile, c.KeyFile); err != nil {
			return err
		}
	}

	var udp []net.PacketConn
	var listeners []net.Listener
	closeAll := func() {
		for _, pc := range udp {
			_ = pc.Close()
		}
		for _, l := range listeners {
			_ = l.Close()
		}
	}
	if c.UDPPort > 0 {
		pc, err := net.ListenPacket("udp4", hostPort(c.ListenIP, c.UDPPort))
		if err != nil {
			closeAll()
			return err
		}
		udp = append(udp, pc)
	}
	if c.TCPPort > 0 {
		l, err := net.Listen("tcp4", hostPort(c.ListenIP, c.TCPPort))
		if err != nil {
			closeAll()
			return err
		}
		listeners = append(listeners, l)
	}
	if c.TLSPort > 0 {
		l, err := tls.Listen("tcp4", hostPort(c.ListenIP, c.TLSPort), certs.tlsConfig())
		if err != nil {
			closeAll()
			return err
		}
		listeners = append(listeners, l)
	}

	srv, err := newServer(c, gen, permitPeers, udp, listeners)
	if err != nil {
		closeAll()
		return err
	}
	defer func() { _ = srv.Close() }()

	limiter := newIPLimiter(c.CredPerMin, c.CredBurst, time.Now)
	mux := http.NewServeMux()
	mux.Handle("/turn-credentials", &credHandler{
		secret: c.Secret, ttl: c.CredTTL, limit: limiter, trustForward: c.TrustForward,
		uris: iceURIs(c.PublicHost, c.UDPPort, c.TCPPort, c.TLSPort),
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "ok %d allocations\n", srv.AllocationCount())
	})

	var httpServers []*http.Server
	errc := make(chan error, 2)
	serve := func(addr string, useTLS bool) {
		hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
		if useTLS {
			hs.TLSConfig = certs.tlsConfig()
		}
		httpServers = append(httpServers, hs)
		go func() {
			var err error
			if useTLS {
				err = hs.ListenAndServeTLS("", "")
			} else {
				err = hs.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("credential endpoint %s: %w", addr, err)
			}
		}()
	}
	if c.HTTPSAddr != "" {
		serve(c.HTTPSAddr, true)
	}
	if c.HTTPAddr != "" {
		serve(c.HTTPAddr, false)
	}
	if len(httpServers) == 0 {
		log.Printf("no credential endpoint: pass -cert/-key (HTTPS on %s) or -http-addr", ":8443")
	}

	// Forget idle rate-limit and bandwidth entries now and then.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				limiter.sweep()
				gen.sweep()
			}
		}
	}()

	log.Printf("toru-relay %s: relay %s ports %d-%d, udp %d, tcp %d, tls %d, uris %v",
		version, c.PublicIP, c.RelayMinPort, c.RelayMaxPort, c.UDPPort, c.TCPPort, c.TLSPort,
		iceURIs(c.PublicHost, c.UDPPort, c.TCPPort, c.TLSPort))

	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, hs := range httpServers {
		_ = hs.Shutdown(shut)
	}
	return err
}

func hostPort(ip string, port int) string { return net.JoinHostPort(ip, strconv.Itoa(port)) }

// certReloader serves the certificate from disk and picks up a renewed one
// (certbot, acme.sh) without a restart: it re-reads the files when their
// modification time changes, checked at most every 30 seconds.
type certReloader struct {
	certFile, keyFile string

	mu      sync.Mutex
	cert    *tls.Certificate
	mod     time.Time
	checked time.Time
}

func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certReloader) load() error {
	st, err := os.Stat(r.certFile)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}
	r.cert, r.mod = &cert, st.ModTime()
	return nil
}

func (r *certReloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.checked) > 30*time.Second {
		r.checked = time.Now()
		if st, err := os.Stat(r.certFile); err == nil && !st.ModTime().Equal(r.mod) {
			if err := r.load(); err != nil {
				log.Printf("certificate reload failed, keeping the old one: %v", err)
			} else {
				log.Printf("certificate reloaded")
			}
		}
	}
	return r.cert, nil
}

func (r *certReloader) tlsConfig() *tls.Config {
	return &tls.Config{GetCertificate: r.get, MinVersion: tls.VersionTLS12}
}
