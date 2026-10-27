//go:build lanselftest

package selftest

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/StephenSHorton/toru/internal/lan/adapter"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// TestSelftest needs an elevated process, an installable toru-lan.exe with
// wintun.dll beside it (TORU_LAN_SELFTEST_EXE), and an output folder
// (TORU_LAN_SELFTEST_OUT, default %TEMP%\toru-lan-selftest). It listens on
// 0.0.0.0:6112 and :4445 for the go/no-go, so the script allows this exact
// binary through the firewall first; run it only through the script.
//
// Every check is recorded; "gate" checks decide pass or fail, the rest are
// information. The report is written even when the test stops early.

type check struct {
	Name   string `json:"name"`
	Gate   bool   `json:"gate"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

type report struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Exe      string    `json:"exe"`
	Version  string    `json:"version"`
	GoNoGo   string    `json:"goNoGo"` // pass | fail | not-run
	Passed   bool      `json:"passed"`
	Checks   []check   `json:"checks"`
	mu       sync.Mutex
	t        *testing.T
}

func (r *report) add(name string, gate bool, err error, detail string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := check{Name: name, Gate: gate, Pass: err == nil, Detail: detail}
	if err != nil {
		if detail != "" {
			c.Detail = detail + ": " + err.Error()
		} else {
			c.Detail = err.Error()
		}
	}
	r.Checks = append(r.Checks, c)
	mark := "PASS"
	if !c.Pass {
		mark = "FAIL"
		if !gate {
			mark = "WARN"
		}
	}
	r.t.Logf("[%s] %s %s", mark, name, c.Detail)
	if gate && !c.Pass {
		r.t.Fail()
	}
	return c.Pass
}

func (r *report) write(dir string) {
	r.Finished = time.Now()
	r.Passed = true
	for _, c := range r.Checks {
		if c.Gate && !c.Pass {
			r.Passed = false
		}
	}
	os.MkdirAll(dir, 0o755)
	b, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(filepath.Join(dir, "report.json"), b, 0o644)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Toru LAN selftest  %s\nexe: %s (%s)\n\n", r.Started.Format(time.RFC1123), r.Exe, r.Version)
	for _, c := range r.Checks {
		mark := "PASS"
		if !c.Pass {
			mark = "FAIL"
			if !c.Gate {
				mark = "warn"
			}
		}
		fmt.Fprintf(&sb, "[%s] %-58s %s\n", mark, c.Name, c.Detail)
	}
	fmt.Fprintf(&sb, "\nbroadcast go/no-go: %s\nresult: %s\n", r.GoNoGo, map[bool]string{true: "PASS", false: "FAIL"}[r.Passed])
	os.WriteFile(filepath.Join(dir, "report.txt"), []byte(sb.String()), 0o644)
}

func TestSelftest(t *testing.T) {
	exe := os.Getenv("TORU_LAN_SELFTEST_EXE")
	out := os.Getenv("TORU_LAN_SELFTEST_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "toru-lan-selftest")
	}
	if exe == "" {
		t.Skip("TORU_LAN_SELFTEST_EXE not set; run scripts/lan-selftest.ps1")
	}
	r := &report{Started: time.Now(), Exe: exe, GoNoGo: "not-run", t: t}
	defer r.write(out)

	if !r.add("process is elevated", true, boolErr(windows.GetCurrentProcessToken().IsElevated(), "not elevated"), "") {
		return
	}
	if err := adapter.HardenDLLSearch(); err != nil {
		r.add("restrict DLL search", true, err, "")
		return
	}
	if v, err := exec.Command(exe, "version").Output(); err == nil {
		r.Version = strings.TrimSpace(string(v))
	}
	pf, _ := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	pd, _ := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	installDir := filepath.Join(pf, api.InstallSubdir)
	dataDir := filepath.Join(pd, api.DataSubdir)

	// Install (also an update when a previous run left it installed).
	outp, err := exec.Command(exe, "install").CombinedOutput()
	if !r.add("install", true, err, strings.TrimSpace(string(outp))) {
		return
	}
	checkInstall(r, installDir, dataDir)

	ip := runService(r)
	if ip.IsValid() {
		r.add("overlay address in 100.96.0.0/16", true, boolErr(adapter.ValidAddr(ip), ip.String()), ip.String())
	}

	goNoGo(r, filepath.Join(filepath.Dir(exe), api.WintunDLL))

	outp, err = exec.Command(exe, "uninstall").CombinedOutput()
	r.add("uninstall", true, err, strings.TrimSpace(string(outp)))
	checkCleanup(r, installDir, dataDir)
}

func boolErr(ok bool, msg string) error {
	if ok {
		return nil
	}
	return errors.New(msg)
}

func checkInstall(r *report, installDir, dataDir string) {
	for _, f := range []string{api.LanExe, api.WintunDLL} {
		_, err := os.Stat(filepath.Join(installDir, f))
		r.add("installed "+f, true, err, installDir)
	}
	r.add("installed wintun.dll hash", true, adapter.VerifySHA256(filepath.Join(installDir, api.WintunDLL), api.WintunSHA256), "")

	sd, err := windows.GetNamedSecurityInfo(dataDir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if r.add("data folder exists", true, err, dataDir) {
		s := sd.String()
		ok := strings.Contains(s, "D:P") && strings.Count(s, "(A;") == 2 && strings.Contains(s, ";;;SY)") && strings.Contains(s, ";;;BA)")
		r.add("data folder ACL is SYSTEM + Administrators only", true, boolErr(ok, s), s)
	}

	m, err := mgr.Connect()
	if !r.add("service manager", true, err, "") {
		return
	}
	defer m.Disconnect()
	s, err := m.OpenService(api.ServiceName)
	if !r.add("service registered", true, err, api.ServiceName) {
		return
	}
	defer s.Close()
	cfg, err := s.Config()
	if r.add("service config readable", true, err, "") {
		r.add("service is demand-start", true, boolErr(cfg.StartType == mgr.StartManual, fmt.Sprint(cfg.StartType)), "")
		r.add("service runs as LocalSystem", true, boolErr(strings.EqualFold(cfg.ServiceStartName, "LocalSystem"), cfg.ServiceStartName), "")
		r.add("service SID type unrestricted", true, boolErr(cfg.SidType == windows.SERVICE_SID_TYPE_UNRESTRICTED, fmt.Sprint(cfg.SidType)), "")
		r.add("service binary in Program Files", true, boolErr(strings.Contains(strings.ToLower(cfg.BinaryPathName), strings.ToLower(installDir)), cfg.BinaryPathName), cfg.BinaryPathName)
	}
	acts, err := s.RecoveryActions()
	r.add("recovery: restart on failure", true, errOr(err, boolErr(len(acts) > 0 && acts[0].Type == mgr.ServiceRestart, fmt.Sprint(acts))), fmt.Sprint(acts))
	ssd, err := windows.GetSecurityInfo(s.Handle, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION)
	if r.add("service security descriptor readable", true, err, "") {
		str := ssd.String()
		iu := ""
		for _, ace := range strings.SplitAfter(str, ")") {
			if strings.HasSuffix(ace, ";;;IU)") {
				iu = ace
			}
		}
		ok := strings.Contains(iu, "RP") && strings.Contains(iu, "WP") && strings.Contains(iu, "LC") && !strings.Contains(iu, "DC") && !strings.Contains(iu, "WD")
		r.add("interactive users may start/stop/query the service (only)", true, boolErr(ok, str), str)
	}

	for _, want := range []struct {
		name  string
		proto int32
		icmp  string
	}{{api.FirewallRuleInbound, 256, ""}, {api.FirewallRuleICMP, 1, "8:*"}} {
		fr, err := adapter.LookupFirewallRule(want.name)
		if !r.add("firewall rule "+want.name+" exists", true, err, "") {
			continue
		}
		ok := fr.Enabled && fr.Direction == 1 && fr.Action == 1 && fr.Protocol == want.proto && fr.PinnedTo(api.AdapterName) &&
			strings.Contains(fr.RemoteAddresses, "100.96.0.0") && fr.Profiles&7 == 7 && fr.ApplicationName == "" && // domain, private, public
			(want.icmp == "" || fr.IcmpTypes == want.icmp)
		r.add("firewall rule "+want.name+" settings", true, boolErr(ok, fmt.Sprintf("%+v", *fr)), fmt.Sprintf("%+v", *fr))
	}
	for _, name := range adapter.BlockRuleNames() {
		fr, err := adapter.LookupFirewallRule(name)
		if !r.add("firewall rule "+name+" exists", true, err, "") {
			continue
		}
		ok := fr.Enabled && fr.Direction == 1 && fr.Action == 0 && fr.Protocol == 6 && fr.PinnedTo(api.AdapterName) &&
			strings.Contains(fr.RemoteAddresses, "100.96.0.0") && fr.LocalPorts == "49152-65535" && fr.ApplicationName != ""
		r.add("firewall rule "+name+" settings", true, boolErr(ok, fmt.Sprintf("%+v", *fr)), fmt.Sprintf("%+v", *fr))
	}
}

func errOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// pipeClient is a minimal GUI: JSON lines in, a channel of messages out.
type pipeClient struct {
	conn net.Conn
	in   chan api.Message
}

func dialPipe() (*pipeClient, error) {
	var conn net.Conn
	var err error
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err = winio.DialPipeAccess(ctx, api.PipeName, api.PipeClientAccess)
		cancel()
		if err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	c := &pipeClient{conn: conn, in: make(chan api.Message, 64)}
	go func() {
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 4096), 1<<20)
		for sc.Scan() {
			var m api.Message
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				c.in <- m
			}
		}
		close(c.in)
	}()
	return c, nil
}

func (c *pipeClient) send(m api.Message) error {
	b, _ := json.Marshal(m)
	_, err := c.conn.Write(append(b, '\n'))
	return err
}

// wait returns the first message matching f.
func (c *pipeClient) wait(d time.Duration, f func(api.Message) bool) (api.Message, error) {
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-c.in:
			if !ok {
				return api.Message{}, errors.New("pipe closed")
			}
			if m.Type == api.TypeError && m.Error != nil {
				return m, fmt.Errorf("service error %s: %s", m.Error.Code, m.Error.Detail)
			}
			if f(m) {
				return m, nil
			}
		case <-deadline:
			return api.Message{}, errors.New("timed out")
		}
	}
}

func waitState(s *mgr.Service, want svc.State, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service state %d, want %d", st.State, want)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// runService starts the service, drives it like the GUI, checks the adapter
// it builds, takes it down, and watches the service stop itself.
func runService(r *report) netip.Addr {
	var ip netip.Addr
	m, err := mgr.Connect()
	if err != nil {
		r.add("service manager", true, err, "")
		return ip
	}
	defer m.Disconnect()
	s, err := m.OpenService(api.ServiceName)
	if err != nil {
		r.add("open service", true, err, "")
		return ip
	}
	defer s.Close()
	err = s.Start()
	if !r.add("service starts", true, errOr(err, waitState(s, svc.Running, 20*time.Second)), "") {
		return ip
	}
	defer func() {
		// Whatever happened, leave it stopped for the go/no-go.
		if st, err := s.Query(); err == nil && st.State != svc.Stopped {
			s.Control(svc.Stop)
			waitState(s, svc.Stopped, 20*time.Second)
		}
	}()

	c, err := dialPipe()
	if !r.add("pipe connects", true, err, api.PipeName) {
		return ip
	}
	defer c.conn.Close()
	c.send(api.Message{Type: api.TypeHello, ID: 1, Hello: &api.Hello{Major: api.ProtocolMajor, Minor: api.ProtocolMinor}})
	hm, err := c.wait(5*time.Second, func(m api.Message) bool { return m.Type == api.TypeHello })
	if !r.add("pipe hello", true, err, "") {
		return ip
	}
	r.add("hello carries version, key and address", true,
		boolErr(hm.Hello.Major == api.ProtocolMajor && hm.Hello.PubKey != "" && hm.Hello.IP != "", fmt.Sprintf("%+v", *hm.Hello)),
		fmt.Sprintf("version %s, ip %s", hm.Hello.Version, hm.Hello.IP))

	c.send(api.Message{Type: api.TypeUp, Up: &api.Up{Room: "selftest", Self: "selftest-gui"}})
	sm, err := c.wait(60*time.Second, func(m api.Message) bool {
		return m.Type == api.TypeStatus && m.Status != nil && (m.Status.State == api.StateOn || m.Status.State == api.StateError)
	})
	if err == nil && sm.Status.State == api.StateError {
		err = fmt.Errorf("%s: %s", sm.Status.Error, sm.Status.Detail)
	}
	if !r.add("Up brings the LAN on", true, err, "") {
		return ip
	}
	ip, _ = netip.ParseAddr(sm.Status.IP)
	r.add("status address matches hello", true, boolErr(sm.Status.IP == hm.Hello.IP, sm.Status.IP+" vs "+hm.Hello.IP), sm.Status.IP)

	checkAdapter(r, ip)

	c.send(api.Message{Type: api.TypeStatus, ID: 7})
	_, err = c.wait(5*time.Second, func(m api.Message) bool { return m.Type == api.TypeStatus && m.ID == 7 })
	r.add("status request answered", true, err, "")

	c.send(api.Message{Type: api.TypeDown})
	_, err = c.wait(20*time.Second, func(m api.Message) bool {
		return m.Type == api.TypeStatus && m.Status != nil && m.Status.State == api.StateOff
	})
	r.add("Down turns the LAN off", true, err, "")
	r.add("adapter removed after Down", true, poll(10*time.Second, func() bool { return !adapter.Exists() }), "")

	// With the GUI gone the service stops itself after 60 s.
	c.conn.Close()
	r.add("service stops itself 60 s after the GUI disconnects", false, waitState(s, svc.Stopped, 90*time.Second), "")
	return ip
}

func poll(d time.Duration, f func() bool) error {
	deadline := time.Now().Add(d)
	for !f() {
		if time.Now().After(deadline) {
			return errors.New("timed out")
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil
}

func checkAdapter(r *report, ip netip.Addr) {
	luid, err := winipcfg.LUIDFromGUID(&adapter.GUID)
	if !r.add("adapter "+api.AdapterName+" exists with the fixed GUID", true, err, api.AdapterGUID) {
		return
	}
	if ifr, err := luid.Interface(); err == nil {
		r.add("adapter alias", true, boolErr(ifr.Alias() == api.AdapterName, ifr.Alias()), ifr.Alias())
	}
	addrs, err := winipcfg.GetUnicastIPAddressTable(windows.AF_INET)
	found := false
	for i := range addrs {
		if addrs[i].InterfaceLUID == luid && addrs[i].Address.Addr() == ip && addrs[i].OnLinkPrefixLength == 16 {
			found = true
		}
	}
	r.add("adapter has "+ip.String()+"/16", true, errOr(err, boolErr(found, "address not found")), "")
	ipif, err := luid.IPInterface(windows.AF_INET)
	if r.add("IPv4 interface readable", true, err, "") {
		r.add("metric 1, automatic metric off", true, boolErr(ipif.Metric == 1 && !ipif.UseAutomaticMetric, fmt.Sprintf("metric %d auto %v", ipif.Metric, ipif.UseAutomaticMetric)), "")
		r.add("NL MTU 1280", true, boolErr(ipif.NLMTU == api.MTU, fmt.Sprint(ipif.NLMTU)), "")
	}
	routes, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	have := map[string]bool{}
	defaultVia := ""
	for i := range routes {
		if routes[i].InterfaceLUID != luid {
			continue
		}
		p := routes[i].DestinationPrefix.Prefix()
		have[p.String()] = true
		if p.Bits() == 0 {
			defaultVia = routes[i].NextHop.Addr().String()
		}
	}
	for _, p := range []string{"255.255.255.255/32", "224.0.0.0/4", "100.96.0.0/16"} {
		r.add("route "+p+" on the adapter", true, errOr(err, boolErr(have[p], "missing")), "")
	}
	if defaultVia != "" {
		r.add("no default route (Unidentified-network fallback in use)", false, fmt.Errorf("default route via %s", defaultVia), "")
	} else {
		r.add("no default route through the overlay", false, nil, "")
	}
	var cat int32
	var catFound bool
	err = poll(60*time.Second, func() bool {
		c, f, err := adapter.Category()
		cat, catFound = c, f
		return err == nil && f && c == adapter.CategoryPrivate
	})
	r.add("network category Private", true, err, fmt.Sprintf("found %v category %d", catFound, cat))
}

// goNoGo is build-order step S1's question: does Windows deliver a
// broadcast written into the Wintun adapter to an ordinary socket bound to
// 0.0.0.0? The service is stopped (Wintun allows one session per adapter),
// so this opens the adapter itself through the same adapter package.
func goNoGo(r *report, dll string) {
	r.GoNoGo = "fail"
	if !r.add("load wintun.dll for the go/no-go", true, adapter.LoadWintun(dll), dll) {
		return
	}
	self := netip.MustParseAddr("100.96.77.77")
	peer := netip.MustParseAddr("100.96.200.200")
	ad, err := adapter.Up(adapter.Config{Addr: self, Logf: r.t.Logf})
	if !r.add("go/no-go adapter up on "+self.String(), true, err, "") {
		return
	}
	defer ad.Close()
	dev := ad.Device()
	r.add("go/no-go address ready", true, poll(15*time.Second, func() bool { return addrReady(ad.LUID(), self) }), "")

	// Port 6112 is Warcraft 3's; any socket bound to 0.0.0.0 stands in for a game.
	recv, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 6112})
	if !r.add("bind 0.0.0.0:6112", true, err, "") {
		return
	}
	defer recv.Close()
	got := make(chan string, 16)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, _, err := recv.ReadFromUDP(buf)
			if err != nil {
				return
			}
			got <- string(buf[:n])
		}
	}()

	pass := true
	for _, c := range []struct {
		name string
		dst  netip.Addr
		gate bool
	}{
		{"unicast to this node (control)", self, false},
		{"limited broadcast 255.255.255.255:6112", netip.MustParseAddr("255.255.255.255"), true},
		{"subnet broadcast 100.96.255.255:6112", netip.MustParseAddr(api.OverlayBcast), true},
	} {
		payload := "toru-selftest " + c.dst.String()
		err := inject(dev, got, udpPacket(peer, c.dst, 50000, 6112, []byte(payload)), payload)
		ok := r.add("injected "+c.name+" reaches a 0.0.0.0 socket", c.gate, err, "")
		if c.gate && !ok {
			pass = false
		}
	}
	if pass {
		r.GoNoGo = "pass"
	}

	multicastCheck(r, dev, peer)
	outboundCheck(r, dev, self)
}

func addrReady(luid winipcfg.LUID, a netip.Addr) bool {
	row, err := luid.IPAddress(a)
	return err == nil && row.DadState == winipcfg.DadStatePreferred
}

// inject writes pkt into the adapter (as if it came from a peer) until
// payload arrives on got, for up to 5 s.
func inject(dev tun.Device, got chan string, pkt []byte, payload string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dev.Write([][]byte{pkt}, 0); err != nil {
			return fmt.Errorf("write to adapter: %w", err)
		}
		timer := time.After(300 * time.Millisecond)
	drain:
		for {
			select {
			case s := <-got:
				if s == payload {
					return nil
				}
			case <-timer:
				break drain
			}
		}
	}
	return errors.New("not received")
}

// multicastCheck is Minecraft Java's discovery: 224.0.2.60:4445, joined on
// the Toru LAN interface. Information only.
func multicastCheck(r *report, dev tun.Device, peer netip.Addr) {
	ifi, err := net.InterfaceByName(api.AdapterName)
	if err != nil {
		r.add("multicast 224.0.2.60:4445 reaches a joined socket", false, err, "interface lookup")
		return
	}
	group := netip.MustParseAddr("224.0.2.60")
	mc, err := net.ListenMulticastUDP("udp4", ifi, &net.UDPAddr{IP: group.AsSlice(), Port: 4445})
	if err != nil {
		r.add("multicast 224.0.2.60:4445 reaches a joined socket", false, err, "join")
		return
	}
	defer mc.Close()
	got := make(chan string, 16)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, _, err := mc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			got <- string(buf[:n])
		}
	}()
	payload := "[MOTD]toru selftest[/MOTD][AD]25565[/AD]"
	err = inject(dev, got, udpPacket(peer, group, 50001, 4445, []byte(payload)), payload)
	r.add("multicast 224.0.2.60:4445 reaches a joined socket", false, err, "")
}

// outboundCheck: a game's broadcast to 255.255.255.255 must leave through
// Toru LAN (metric 1 plus the /32 route). Information only: another metric-1
// adapter (Radmin VPN) can tie.
func outboundCheck(r *report, dev tun.Device, self netip.Addr) {
	payload := []byte("toru-selftest outbound")
	seen := make(chan struct{}, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		bufs := [][]byte{make([]byte, 65535)}
		sizes := []int{0}
		for {
			n, err := dev.Read(bufs, sizes, 0)
			if err != nil {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
			for i := 0; i < n; i++ {
				p := bufs[i][:sizes[i]]
				if len(p) >= 28 && p[0]>>4 == 4 && p[9] == 17 && netip.AddrFrom4([4]byte(p[16:20])) == netip.MustParseAddr("255.255.255.255") &&
					binary.BigEndian.Uint16(p[22:24]) == 6112 && strings.Contains(string(p[28:]), string(payload)) {
					select {
					case seen <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4bcast, Port: 6112})
	if err != nil {
		r.add("outbound 255.255.255.255 leaves through Toru LAN", false, err, "")
		return
	}
	defer conn.Close()
	var res error = errors.New("not seen on the adapter (another adapter won the route)")
	for i := 0; i < 10; i++ {
		conn.Write(payload)
		select {
		case <-seen:
			res = nil
		case <-time.After(300 * time.Millisecond):
			continue
		}
		break
	}
	r.add("outbound 255.255.255.255 leaves through Toru LAN", false, res, "")
}

// udpPacket builds an IPv4/UDP packet with valid checksums, TTL 1 (like
// LAN discovery traffic).
func udpPacket(src, dst netip.Addr, sport, dport uint16, payload []byte) []byte {
	const ihl = 20
	p := make([]byte, ihl+8+len(payload))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	binary.BigEndian.PutUint16(p[4:], 0x1234)
	p[8] = 1 // TTL
	p[9] = 17
	s, d := src.As4(), dst.As4()
	copy(p[12:16], s[:])
	copy(p[16:20], d[:])
	binary.BigEndian.PutUint16(p[10:], checksum(p[:ihl], 0))
	u := p[ihl:]
	binary.BigEndian.PutUint16(u[0:], sport)
	binary.BigEndian.PutUint16(u[2:], dport)
	binary.BigEndian.PutUint16(u[4:], uint16(8+len(payload)))
	copy(u[8:], payload)
	// Pseudo-header: src, dst, zero, protocol, UDP length.
	var ph uint32
	for i := 0; i < 4; i += 2 {
		ph += uint32(binary.BigEndian.Uint16(s[i:])) + uint32(binary.BigEndian.Uint16(d[i:]))
	}
	ph += 17 + uint32(len(u))
	cs := checksum(u, ph)
	if cs == 0 {
		cs = 0xffff
	}
	binary.BigEndian.PutUint16(u[6:], cs)
	return p
}

func checksum(b []byte, initial uint32) uint16 {
	sum := initial
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func checkCleanup(r *report, installDir, dataDir string) {
	m, err := mgr.Connect()
	if err == nil {
		s, err := m.OpenService(api.ServiceName)
		if err == nil {
			s.Close()
		}
		r.add("service deleted", true, boolErr(err != nil, "still registered"), "")
		m.Disconnect()
	}
	for _, name := range adapter.FirewallRuleNames() {
		_, err := adapter.LookupFirewallRule(name)
		r.add("firewall rule "+name+" removed", true, boolErr(err != nil, "still present"), "")
	}
	r.add("adapter gone", true, poll(10*time.Second, func() bool { return !adapter.Exists() }), "")
	_, err = os.Stat(dataDir)
	r.add("data folder removed", true, boolErr(os.IsNotExist(err), dataDir), dataDir)
	// The uninstaller deletes its own folder a few seconds after it exits.
	r.add("install folder removed", true, poll(15*time.Second, func() bool {
		_, err := os.Stat(installDir)
		return os.IsNotExist(err)
	}), installDir)
}

// TestPacketChecksum keeps the packet builder honest without elevation; it
// still only builds under the lanselftest tag.
func TestPacketChecksum(t *testing.T) {
	p := udpPacket(netip.MustParseAddr("100.96.200.200"), netip.MustParseAddr("255.255.255.255"), 50000, 6112, []byte("hi"))
	if checksum(p[:20], 0) != 0 {
		t.Fatal("IPv4 header checksum does not verify")
	}
	s, d := [4]byte(p[12:16]), [4]byte(p[16:20])
	var ph uint32
	for i := 0; i < 4; i += 2 {
		ph += uint32(binary.BigEndian.Uint16(s[i:])) + uint32(binary.BigEndian.Uint16(d[i:]))
	}
	ph += 17 + uint32(len(p)-20)
	if checksum(p[20:], ph) != 0 {
		t.Fatal("UDP checksum does not verify")
	}
}
