package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// adapter_windows.go is the Wintun side. Wintun's own SDDL lets only SYSTEM
// or an elevated process open adapters, so everything here runs in the
// service (or the elevated installer and selftest).

// Routing constants. The two on-link routes make Winsock send limited
// broadcast and multicast out of Toru LAN while it has the lowest metric
// (docs/lan.md, "The Windows limited-broadcast quirk").
var (
	limitedBcast = netip.MustParsePrefix("255.255.255.255/32")
	multicast    = netip.MustParsePrefix("224.0.0.0/4")
	// defaultRoute via dummyGateway is the Unidentified-network fallback
	// (Radmin's trick): a default route that is never used for traffic
	// (metric 9256) but gives NLA a gateway to identify the network by.
	defaultRoute = netip.MustParsePrefix("0.0.0.0/0")
	dummyGateway = netip.MustParseAddr("100.96.0.1")
)

const dummyGatewayMetric = 9256

// GUID is api.AdapterGUID parsed.
var GUID = func() windows.GUID {
	g, err := windows.GUIDFromString(api.AdapterGUID)
	if err != nil {
		panic(err)
	}
	return g
}()

// HardenDLLSearch restricts every later DLL load in this process to System32
// and the exe's own folder (Tailscale does the same at service start), so a
// DLL planted in the working directory or on PATH is never picked up. Call it
// first thing in main.
func HardenDLLSearch() error {
	return windows.SetDefaultDllDirectories(windows.LOAD_LIBRARY_SEARCH_SYSTEM32 | windows.LOAD_LIBRARY_SEARCH_APPLICATION_DIR)
}

var (
	wintunOnce sync.Once
	wintunErr  error
)

// LoadWintun checks path's SHA-256 against api.WintunSHA256 and loads it by
// full path. The wintun package later asks for "wintun.dll" by base name,
// and the loader hands back this already-loaded module. Only the first call
// in a process does anything.
func LoadWintun(path string) error {
	wintunOnce.Do(func() {
		if err := VerifySHA256(path, api.WintunSHA256); err != nil {
			wintunErr = err
			return
		}
		const searchFlags = windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | windows.LOAD_LIBRARY_SEARCH_SYSTEM32
		if _, err := windows.LoadLibraryEx(path, 0, searchFlags); err != nil {
			wintunErr = fmt.Errorf("load %s: %w", path, err)
		}
	})
	return wintunErr
}

// VerifySHA256 fails unless the file at path hashes to want (lowercase hex).
func VerifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: SHA-256 %s, want %s", path, got, want)
	}
	return nil
}

// Config is what Up needs. The address is the only per-node input; name,
// GUID, MTU and routes are fixed.
type Config struct {
	Addr netip.Addr
	Logf func(format string, args ...any)
}

// Adapter is the live Toru LAN adapter. Device is what the WireGuard side
// reads and writes; Close removes the adapter.
type Adapter struct {
	dev  tun.Device
	luid winipcfg.LUID
	logf func(format string, args ...any)

	mu       sync.Mutex
	addr     netip.Addr
	closed   bool
	stop     chan struct{}
	done     chan struct{}
	fallback bool // dummy gateway route added
}

// Up checks the overlay range, creates the adapter and configures it. The
// Private category is applied in the background (NLA can take many seconds
// to identify a new network); Up does not wait for it.
func Up(cfg Config) (*Adapter, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if !ValidAddr(cfg.Addr) {
		return nil, fmt.Errorf("address %s is not a node address in %s", cfg.Addr, Overlay)
	}
	if err := CheckRange(); err != nil {
		return nil, err
	}

	tun.WintunTunnelType = api.TunnelType
	guid := GUID
	dev, err := tun.CreateTUNWithRequestedGUID(api.AdapterName, &guid, api.MTU)
	if err != nil {
		return nil, err
	}
	nt, ok := dev.(*tun.NativeTun)
	if !ok {
		_ = dev.Close()
		return nil, errors.New("not a Wintun device")
	}
	a := &Adapter{
		dev:  dev,
		luid: winipcfg.LUID(nt.LUID()),
		logf: logf,
		addr: cfg.Addr,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	if err := a.configure(); err != nil {
		_ = dev.Close()
		return nil, err
	}
	go a.privateLoop()
	return a, nil
}

// Device is the Wintun tun.Device.
func (a *Adapter) Device() tun.Device { return a.dev }

// LUID is the adapter's interface LUID.
func (a *Adapter) LUID() winipcfg.LUID { return a.luid }

// Addr is the configured overlay address.
func (a *Adapter) Addr() netip.Addr {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

// FallbackGateway reports whether the Unidentified-network fallback route
// had to be added.
func (a *Adapter) FallbackGateway() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.fallback
}

// SetAddr replaces the overlay address (Readdress after a collision).
func (a *Adapter) SetAddr(addr netip.Addr) error {
	if !ValidAddr(addr) {
		return fmt.Errorf("address %s is not a node address in %s", addr, Overlay)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.luid.SetIPAddressesForFamily(windows.AF_INET, []netip.Prefix{netip.PrefixFrom(addr, Overlay.Bits())}); err != nil {
		return fmt.Errorf("set address: %w", err)
	}
	a.addr = addr
	return nil
}

// configure applies address, routes and the IPv4 interface settings. A just
// created adapter can briefly report ERROR_NOT_FOUND; retry for a while, as
// wireguard-windows does after boot.
func (a *Adapter) configure() error {
	var err error
	for try := 0; try < 15; try++ {
		if try > 0 {
			time.Sleep(time.Second)
		}
		err = a.configureOnce()
		if !errors.Is(err, windows.ERROR_NOT_FOUND) {
			break
		}
		a.logf("adapter: configure try %d: %v", try, err)
	}
	return err
}

func (a *Adapter) configureOnce() error {
	addrs := []netip.Prefix{netip.PrefixFrom(a.addr, Overlay.Bits())}
	err := a.luid.SetIPAddressesForFamily(windows.AF_INET, addrs)
	if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
		// The address is still on a disconnected interface (a crashed run
		// whose adapter was not cleaned up yet). Take it off and retry.
		removeStaleAddress(a.addr)
		err = a.luid.SetIPAddressesForFamily(windows.AF_INET, addrs)
	}
	if err != nil {
		return fmt.Errorf("set address: %w", err)
	}
	// Windows creates its own on-link 255.255.255.255/32 and 224.0.0.0/4
	// routes (route metric 256) on every interface once it has an address, so
	// the add usually finds one: lower it to 0 instead. Another interface-
	// metric-1 adapter (Radmin VPN) then loses the tie, 1+0 against 1+256, and
	// unbound broadcasts and multicasts leave through Toru LAN.
	for _, p := range []netip.Prefix{limitedBcast, multicast} {
		err := a.luid.AddRoute(p, netip.IPv4Unspecified(), 0)
		if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
			var row *winipcfg.MibIPforwardRow2
			if row, err = a.luid.Route(p, netip.IPv4Unspecified()); err == nil && row.Metric != 0 {
				row.Metric = 0
				err = row.Set()
			}
		}
		if err != nil {
			return fmt.Errorf("add route %s: %w", p, err)
		}
	}
	ipif, err := a.luid.IPInterface(windows.AF_INET)
	if err != nil {
		return fmt.Errorf("get IPv4 interface: %w", err)
	}
	ipif.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
	ipif.DadTransmits = 0
	ipif.ManagedAddressConfigurationSupported = false
	ipif.OtherStatefulConfigurationSupported = false
	ipif.UseAutomaticMetric = false
	ipif.Metric = 1
	ipif.NLMTU = api.MTU
	if err := ipif.Set(); err != nil {
		return fmt.Errorf("set metric and MTU: %w", err)
	}
	return nil
}

func removeStaleAddress(addr netip.Addr) {
	ifaces, err := winipcfg.GetAdaptersAddresses(windows.AF_INET, winipcfg.GAAFlagDefault)
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if iface.OperStatus == winipcfg.IfOperStatusUp {
			continue
		}
		for u := iface.FirstUnicastAddress; u != nil; u = u.Next {
			if ip, ok := netip.AddrFromSlice(u.Address.IP()); ok && ip.Unmap() == addr {
				_ = iface.LUID.DeleteIPAddress(netip.PrefixFrom(addr, int(u.OnLinkPrefixLength)))
			}
		}
	}
}

// privateLoop marks the network Private, retrying while NLA identifies it.
// If NLA has not produced a network for the adapter after the first round,
// it adds the dummy gateway route (which lets NLA identify an otherwise
// gateway-less network) and tries again.
func (a *Adapter) privateLoop() {
	defer close(a.done)
	const tries = 20
	for round := 0; round < 2; round++ {
		for i := 0; i < tries; i++ {
			found, err := setPrivate(GUID)
			switch {
			case err != nil:
				a.logf("adapter: set Private (round %d, try %d): %v", round, i, err)
			case found:
				a.logf("adapter: network category Private (round %d, try %d)", round, i)
				return
			}
			select {
			case <-a.stop:
				return
			case <-time.After(time.Second):
			}
		}
		if round == 0 {
			a.logf("adapter: network still unidentified; adding dummy gateway route")
			if err := a.luid.AddRoute(defaultRoute, dummyGateway, dummyGatewayMetric); err != nil && !errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
				a.logf("adapter: dummy gateway route: %v", err)
				return
			}
			a.mu.Lock()
			a.fallback = true
			a.mu.Unlock()
		}
	}
	a.logf("adapter: gave up on the Private category; inbound game traffic may be blocked")
}

// Close puts the metric back to automatic and removes the adapter. Safe to
// call more than once.
func (a *Adapter) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	close(a.stop)
	a.mu.Unlock()
	<-a.done
	// The adapter is about to disappear, so this matters only if removal
	// fails: Toru LAN must not keep metric 1 when LAN is off.
	if ipif, err := a.luid.IPInterface(windows.AF_INET); err == nil {
		ipif.UseAutomaticMetric = true
		if err := ipif.Set(); err != nil {
			a.logf("adapter: restore metric: %v", err)
		}
	}
	return a.dev.Close()
}

// CheckRange fails with *RangeInUseError if any other interface routes part
// of 100.96.0.0/16 (Tailscale, a carrier CGNAT leaking onto the LAN).
func CheckRange() error {
	rows, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return fmt.Errorf("read routes: %w", err)
	}
	own, ownErr := winipcfg.LUIDFromGUID(&GUID)
	routes := make([]Route, 0, len(rows))
	for i := range rows {
		r := Route{Prefix: rows[i].DestinationPrefix.Prefix(), Ours: ownErr == nil && rows[i].InterfaceLUID == own}
		routes = append(routes, r)
	}
	r, ok := findOverlap(Overlay, routes)
	if !ok {
		return nil
	}
	// Name the owner only for the hit; looking up every row is wasted work.
	for i := range rows {
		if rows[i].DestinationPrefix.Prefix() == r.Prefix {
			if ifr, err := rows[i].InterfaceLUID.Interface(); err == nil {
				r.Alias = ifr.Alias()
			}
			break
		}
	}
	return &RangeInUseError{Prefix: r.Prefix, Alias: r.Alias}
}

// WithBareAdapter runs fn while the Toru LAN adapter exists, creating it
// unconfigured for the duration if it is not there (install uses this to
// pin the firewall rules to the adapter). fn is retried for a few seconds:
// a just-created adapter takes a moment to become visible by name.
func WithBareAdapter(fn func() error) error {
	if !Exists() {
		tun.WintunTunnelType = api.TunnelType
		guid := GUID
		ad, err := wintun.CreateAdapter(api.AdapterName, api.TunnelType, &guid)
		if err != nil {
			return fmt.Errorf("create adapter: %w", err)
		}
		defer func() { _ = ad.Close() }()
	}
	var err error
	for try := 0; try < 20; try++ {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

// Exists reports whether the Toru LAN adapter is present.
func Exists() bool {
	_, err := winipcfg.LUIDFromGUID(&GUID)
	return err == nil
}

// Remove deletes a leftover Toru LAN adapter (a crashed service) and, when no
// other Wintun adapter remains, the Wintun driver. Used by uninstall; the
// caller must have loaded wintun.dll. Wintun removes an adapter when the
// handle that created it closes, and its create call cleans up orphans, so
// create-then-close is the removal path.
func Remove() error {
	if Exists() {
		guid := GUID
		ad, err := wintun.CreateAdapter(api.AdapterName, api.TunnelType, &guid)
		if err != nil {
			return fmt.Errorf("open leftover adapter: %w", err)
		}
		_ = ad.Close()
	}
	if Exists() {
		return errors.New("adapter still present after removal")
	}
	// WintunDeleteDriver refuses while any Wintun adapter exists (another
	// VPN), which is what we want; that is not an error for us.
	_ = wintun.Uninstall()
	return nil
}
