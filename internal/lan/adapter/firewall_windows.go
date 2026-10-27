package adapter

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

// firewall_windows.go creates and removes the two Windows Firewall rules
// through INetFwPolicy2 (COM; no netsh fallback, since netsh cannot pin a
// rule to an interface alias). Both rules are pinned to the Toru LAN
// adapter, so a physical NIC never gets them, and to the overlay range:
//
//   - Toru LAN (inbound): allow, all programs, any protocol, all profiles.
//   - Toru LAN ICMP: echo requests, so Ping works.
//
// Changing the policy needs an elevated or SYSTEM caller; building rule
// objects does not, which is what the unprivileged tests rely on.

// Rule values from icftypes.h.
const (
	fwDirIn         = 1
	fwActionAllow   = 1
	fwProfilesAll   = 0x7fffffff
	fwProtocolAny   = 256
	fwProtocolICMP4 = 1
)

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

// EnsureFirewallRules replaces both rules with fresh copies (idempotent; a
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
		for _, name := range []string{api.FirewallRuleInbound, api.FirewallRuleICMP} {
			removeAll(rules, name)
		}
		if err := addRule(rules, api.FirewallRuleInbound, fwProtocolAny, ""); err != nil {
			return err
		}
		return addRule(rules, api.FirewallRuleICMP, fwProtocolICMP4, "8:*")
	})
}

// RulesPresent reports whether both rules exist (no repair).
func RulesPresent() (bool, error) {
	ok := true
	err := withCOM(func() error {
		rules, release, err := fwRules()
		if err != nil {
			return err
		}
		defer release()
		for _, name := range []string{api.FirewallRuleInbound, api.FirewallRuleICMP} {
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

// RemoveFirewallRules deletes both rules (every copy, if a user duplicated
// one). Missing rules are not an error.
func RemoveFirewallRules() error {
	return withCOM(func() error {
		rules, release, err := fwRules()
		if err != nil {
			return err
		}
		defer release()
		for _, name := range []string{api.FirewallRuleInbound, api.FirewallRuleICMP} {
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

func addRule(rules *ole.IDispatch, name string, protocol int32, icmp string) error {
	rule, err := newRule(name, protocol, icmp, api.AdapterName)
	if err != nil {
		return err
	}
	defer rule.Release()
	if _, err := oleutil.CallMethod(rules, "Add", rule); err != nil {
		return fmt.Errorf("add rule %q: %w", name, err)
	}
	return nil
}

// newRule builds (but does not add) one rule object pinned to the interface
// named iface. Protocol goes before IcmpTypesAndCodes, which the firewall
// validates against it. Setting Interfaces fails with "Element not found"
// unless an interface by that name exists right now.
func newRule(name string, protocol int32, icmp, iface string) (*ole.IDispatch, error) {
	rule, err := createDispatch("HNetCfg.FWRule")
	if err != nil {
		return nil, err
	}
	props := []struct {
		name string
		val  any
	}{
		{"Name", name},
		{"Description", "Toru LAN: lets room members reach this PC over the Toru LAN adapter only."},
		{"Grouping", "Toru LAN"},
		{"Protocol", protocol},
		{"Direction", int32(fwDirIn)},
		{"Action", int32(fwActionAllow)},
		{"Profiles", int32(fwProfilesAll)},
		{"RemoteAddresses", remoteRange},
	}
	if icmp != "" {
		props = append(props, struct {
			name string
			val  any
		}{"IcmpTypesAndCodes", icmp})
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
	}{{"RemoteAddresses", &r.RemoteAddresses}, {"IcmpTypesAndCodes", &r.IcmpTypes}, {"ApplicationName", &r.ApplicationName}}
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
// iface (api.AdapterName for the real rules).
func (r *FirewallRule) PinnedTo(iface string) bool {
	return len(r.Interfaces) == 1 && strings.EqualFold(r.Interfaces[0], iface)
}
