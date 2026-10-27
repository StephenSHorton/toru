package adapter

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

// firewall_windows.go creates and removes the Windows Firewall rules
// through INetFwPolicy2 (COM; no netsh fallback, since netsh cannot pin a
// rule to an interface alias). Every rule is pinned to the Toru LAN
// adapter, so a physical NIC never gets them, and to the overlay range:
//
//   - Toru LAN (inbound): allow, all programs, any protocol, all profiles.
//   - Toru LAN ICMP: echo requests, so Ping works.
//   - Toru LAN (block system RPC) <program>: block inbound TCP to the
//     dynamic RPC range (49152-65535) for each Windows process that serves
//     RPC there (svchost, lsass, wininit, services, spoolsv). Those endpoints
//     need no endpoint mapper, so the shim's port 135 block alone leaves them
//     open; a game's own server on those ports is another program and is
//     unaffected. Block rules win over the allow rule.
//
// Changing the policy needs an elevated or SYSTEM caller; building rule
// objects does not, which is what the unprivileged tests rely on.

// Rule values from icftypes.h.
const (
	fwDirIn         = 1
	fwActionBlock   = 0
	fwActionAllow   = 1
	fwProfilesAll   = 0x7fffffff
	fwProtocolAny   = 256
	fwProtocolICMP4 = 1
	fwProtocolTCP   = 6
)

// dynamicRPCPorts is Windows' dynamic RPC range (the IANA ephemeral range).
const dynamicRPCPorts = "49152-65535"

// systemRPCHosts are the System32 programs that serve RPC on the dynamic range.
var systemRPCHosts = []string{"svchost.exe", "lsass.exe", "wininit.exe", "services.exe", "spoolsv.exe"}

// ruleSpec is one rule to create.
type ruleSpec struct {
	name       string
	protocol   int32
	icmp       string // ICMP types and codes ("" = none)
	block      bool
	localPorts string // "" = any
	app        string // full program path ("" = all programs)
}

// ruleSpecs are every rule EnsureFirewallRules creates.
func ruleSpecs() []ruleSpec {
	specs := []ruleSpec{
		{name: api.FirewallRuleInbound, protocol: fwProtocolAny},
		{name: api.FirewallRuleICMP, protocol: fwProtocolICMP4, icmp: "8:*"},
	}
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		sys = `C:\Windows\System32`
	}
	for _, exe := range systemRPCHosts {
		specs = append(specs, ruleSpec{
			name: BlockRuleName(exe), protocol: fwProtocolTCP, block: true,
			localPorts: dynamicRPCPorts, app: filepath.Join(sys, exe),
		})
	}
	return specs
}

// BlockRuleName is the name of the system-RPC block rule for exe.
func BlockRuleName(exe string) string {
	return api.FirewallRuleBlockRPC + " " + strings.TrimSuffix(exe, ".exe")
}

// FirewallRuleNames lists every rule the installer creates.
func FirewallRuleNames() []string {
	var out []string
	for _, s := range ruleSpecs() {
		out = append(out, s.name)
	}
	return out
}

// BlockRuleNames lists the system-RPC block rules.
func BlockRuleNames() []string {
	var out []string
	for _, exe := range systemRPCHosts {
		out = append(out, BlockRuleName(exe))
	}
	return out
}

// remoteRange is the overlay in the mask form the firewall echoes back.
const remoteRange = "100.96.0.0/255.255.0.0"

// FirewallRule is what the selftest reads back from an installed rule.
type FirewallRule struct {
	Name            string
	Enabled         bool
	Direction       int32
	Action          int32
	Protocol        int32
	Profiles        int32
	RemoteAddresses string
	LocalPorts      string
	IcmpTypes       string
	Interfaces      []string
	ApplicationName string
}

// iNetFwRule's put_Interfaces sits after IDispatch and the getters/setters
// for Name, Description, ApplicationName, ServiceName, Protocol, LocalPorts,
// RemotePorts, LocalAddresses, RemoteAddresses, IcmpTypesAndCodes, Direction
// and get_Interfaces (netfw.h).
type iNetFwRuleVtbl struct {
	ole.IDispatchVtbl
	_             [23]uintptr // get_Name .. get_Interfaces
	PutInterfaces uintptr
}

var iidINetFwRule = ole.NewGUID("{AF230D27-BABA-4E42-ACED-F524F22CFCE2}")

var (
	oleaut32                = windows.NewLazySystemDLL("oleaut32.dll")
	procSafeArrayCreateVect = oleaut32.NewProc("SafeArrayCreateVector")
	procSafeArrayPutElement = oleaut32.NewProc("SafeArrayPutElement")
)

// EnsureFirewallRules replaces every rule with a fresh copy (idempotent; a
// re-run fixes a rule a user edited). The firewall resolves Interfaces when
// the rule is built, so the adapter must exist when this runs: install
// brings it up briefly for this, and the service repairs missing rules on
// every Up. The adapter's fixed GUID keeps the rule matching across
// re-creations.
func EnsureFirewallRules() error {
	return withCOM(func() error {
		rules, release, err := fwRules()
		if err != nil {
			return err
		}
		defer release()
		specs := ruleSpecs()
		for _, s := range specs {
			removeAll(rules, s.name)
		}
		// Block rules first, so the allow rule never stands without them.
		for i := len(specs) - 1; i >= 0; i-- {
			if err := addRule(rules, specs[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// RulesPresent reports whether every rule exists (no repair).
func RulesPresent() (bool, error) {
	ok := true
	err := withCOM(func() error {
		rules, release, err := fwRules()
		if err != nil {
			return err
		}
		defer release()
		for _, name := range FirewallRuleNames() {
			v, err := oleutil.CallMethod(rules, "Item", name)
			if err != nil {
				ok = false
				continue
			}
			v.Clear()
		}
		return nil
	})
	return ok, err
}

// RemoveFirewallRules deletes every rule (every copy, if a user duplicated
// one). Missing rules are not an error.
func RemoveFirewallRules() error {
	return withCOM(func() error {
		rules, release, err := fwRules()
		if err != nil {
			return err
		}
		defer release()
		for _, name := range FirewallRuleNames() {
			removeAll(rules, name)
		}
		return nil
	})
}

// LookupFirewallRule reads one rule by name.
func LookupFirewallRule(name string) (*FirewallRule, error) {
	var out *FirewallRule
	err := withCOM(func() error {
		rules, release, err := fwRules()
		if err != nil {
			return err
		}
		defer release()
		v, err := oleutil.CallMethod(rules, "Item", name)
		if err != nil {
			return fmt.Errorf("rule %q: %w", name, err)
		}
		defer v.Clear()
		out, err = readRule(v.ToIDispatch())
		return err
	})
	return out, err
}

func fwRules() (*ole.IDispatch, func(), error) {
	policy, err := createDispatch("HNetCfg.FwPolicy2")
	if err != nil {
		return nil, nil, err
	}
	v, err := oleutil.GetProperty(policy, "Rules")
	if err != nil {
		policy.Release()
		return nil, nil, fmt.Errorf("FwPolicy2.Rules: %w", err)
	}
	return v.ToIDispatch(), func() { v.Clear(); policy.Release() }, nil
}

func createDispatch(progID string) (*ole.IDispatch, error) {
	unk, err := oleutil.CreateObject(progID)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", progID, err)
	}
	defer unk.Release()
	return unk.QueryInterface(ole.IID_IDispatch)
}

func removeAll(rules *ole.IDispatch, name string) {
	for i := 0; i < 16; i++ {
		v, err := oleutil.CallMethod(rules, "Item", name)
		if err != nil {
			return // not found
		}
		v.Clear()
		if _, err := oleutil.CallMethod(rules, "Remove", name); err != nil {
			return
		}
	}
}

func addRule(rules *ole.IDispatch, spec ruleSpec) error {
	rule, err := newRule(spec, api.AdapterName)
	if err != nil {
		return err
	}
	defer rule.Release()
	if _, err := oleutil.CallMethod(rules, "Add", rule); err != nil {
		return fmt.Errorf("add rule %q: %w", spec.name, err)
	}
	return nil
}

// newRule builds (but does not add) one rule object pinned to the interface
// named iface. Protocol goes before IcmpTypesAndCodes and LocalPorts, which
// the firewall validates against it. Setting Interfaces fails with "Element
// not found" unless an interface by that name exists right now.
func newRule(spec ruleSpec, iface string) (*ole.IDispatch, error) {
	name := spec.name
	rule, err := createDispatch("HNetCfg.FWRule")
	if err != nil {
		return nil, err
	}
	type prop struct {
		name string
		val  any
	}
	action, desc := int32(fwActionAllow), "Toru LAN: lets room members reach this PC over the Toru LAN adapter only."
	if spec.block {
		action, desc = fwActionBlock, "Toru LAN: keeps Windows' own RPC services closed to room members."
	}
	props := []prop{
		{"Name", name},
		{"Description", desc},
		{"Grouping", "Toru LAN"},
		{"Protocol", spec.protocol},
		{"Direction", int32(fwDirIn)},
		{"Action", action},
		{"Profiles", int32(fwProfilesAll)},
		{"RemoteAddresses", remoteRange},
	}
	if spec.icmp != "" {
		props = append(props, prop{"IcmpTypesAndCodes", spec.icmp})
	}
	if spec.localPorts != "" {
		props = append(props, prop{"LocalPorts", spec.localPorts})
	}
	if spec.app != "" {
		props = append(props, prop{"ApplicationName", spec.app})
	}
	for _, p := range props {
		if _, err := oleutil.PutProperty(rule, p.name, p.val); err != nil {
			rule.Release()
			return nil, fmt.Errorf("rule %q: set %s: %w", name, p.name, err)
		}
	}
	if err := putInterfaces(rule, []string{iface}); err != nil {
		rule.Release()
		return nil, fmt.Errorf("rule %q: set Interfaces: %w", name, err)
	}
	if _, err := oleutil.PutProperty(rule, "Enabled", true); err != nil {
		rule.Release()
		return nil, fmt.Errorf("rule %q: enable: %w", name, err)
	}
	return rule, nil
}

// putInterfaces sets INetFwRule.Interfaces, which takes a VARIANT holding a
// SAFEARRAY of VARIANT(BSTR). go-ole's []string path builds a SAFEARRAY of
// BSTR instead, which the firewall rejects, so this builds the array and
// calls the vtable slot directly.
func putInterfaces(rule *ole.IDispatch, names []string) error {
	unk, err := rule.QueryInterface(iidINetFwRule)
	if err != nil {
		return err
	}
	defer unk.Release()

	psa, _, _ := procSafeArrayCreateVect.Call(uintptr(ole.VT_VARIANT), 0, uintptr(len(names)))
	if psa == 0 {
		return errors.New("SafeArrayCreateVector failed")
	}
	arr := ole.NewVariant(ole.VT_ARRAY|ole.VT_VARIANT, int64(psa))
	defer ole.VariantClear(&arr) // frees the array and the BSTR copies in it
	for i, n := range names {
		elem := ole.NewVariant(ole.VT_BSTR, int64(uintptr(unsafe.Pointer(ole.SysAllocStringLen(n)))))
		idx := int32(i)
		r, _, _ := procSafeArrayPutElement.Call(psa, uintptr(unsafe.Pointer(&idx)), uintptr(unsafe.Pointer(&elem)))
		ole.VariantClear(&elem) // SafeArrayPutElement copied it
		if err := hresult(r); err != nil {
			return fmt.Errorf("SafeArrayPutElement: %w", err)
		}
	}
	vt := (*iNetFwRuleVtbl)(unsafe.Pointer(unk.RawVTable))
	// VARIANT is larger than a register, so the x64 ABI passes it by pointer.
	return hresult(syscallN(vt.PutInterfaces, uintptr(unsafe.Pointer(unk)), uintptr(unsafe.Pointer(&arr))))
}

func readRule(rule *ole.IDispatch) (*FirewallRule, error) {
	if rule == nil {
		return nil, errors.New("rule is not IDispatch")
	}
	r := &FirewallRule{}
	get := func(name string) *ole.VARIANT {
		v, err := oleutil.GetProperty(rule, name)
		if err != nil {
			return nil
		}
		return v
	}
	if v := get("Name"); v != nil {
		r.Name = v.ToString()
		v.Clear()
	}
	if v := get("Enabled"); v != nil {
		r.Enabled, _ = v.Value().(bool)
		v.Clear()
	}
	ints := []struct {
		name string
		dst  *int32
	}{{"Direction", &r.Direction}, {"Action", &r.Action}, {"Protocol", &r.Protocol}, {"Profiles", &r.Profiles}}
	for _, p := range ints {
		if v := get(p.name); v != nil {
			*p.dst = int32(v.Val)
			v.Clear()
		}
	}
	strs := []struct {
		name string
		dst  *string
	}{{"RemoteAddresses", &r.RemoteAddresses}, {"LocalPorts", &r.LocalPorts}, {"IcmpTypesAndCodes", &r.IcmpTypes}, {"ApplicationName", &r.ApplicationName}}
	for _, p := range strs {
		if v := get(p.name); v != nil {
			if v.VT == ole.VT_BSTR {
				*p.dst = v.ToString()
			}
			v.Clear()
		}
	}
	if v := get("Interfaces"); v != nil {
		if v.VT&ole.VT_ARRAY != 0 {
			for _, e := range v.ToArray().ToValueArray() {
				if s, ok := e.(string); ok {
					r.Interfaces = append(r.Interfaces, s)
				}
			}
		}
		v.Clear()
	}
	return r, nil
}

// PinnedTo reports whether a rule is pinned to exactly the interface named
// iface (api.AdapterName for the real rules). The firewall stores the
// interface by GUID once it resolves the name (seen on Windows 11 26200), so
// for the Toru LAN adapter its fixed GUID counts as the same interface.
func (r *FirewallRule) PinnedTo(iface string) bool {
	if len(r.Interfaces) != 1 {
		return false
	}
	got := r.Interfaces[0]
	return strings.EqualFold(got, iface) || (strings.EqualFold(iface, api.AdapterName) && strings.EqualFold(got, api.AdapterGUID))
}
