package adapter

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// These tests need no elevation and open no sockets: they read the routing
// table, enumerate NLM connections, and build (never add) firewall rules.

func repoFile(t *testing.T, rel string) string {
	_, here, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(here), "..", "..", "..", filepath.FromSlash(rel))
}

func TestVendoredWintunHash(t *testing.T) {
	if err := VerifySHA256(repoFile(t, "build/windows/wintun/wintun.dll"), api.WintunSHA256); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(repoFile(t, "build/windows/wintun/LICENSE.txt"), api.WintunSHA256); err == nil {
		t.Fatal("a different file passed the wintun hash check")
	}
}

func TestGUIDParses(t *testing.T) {
	if !strings.EqualFold(GUID.String(), api.AdapterGUID) {
		t.Fatalf("GUID %s != %s", GUID, api.AdapterGUID)
	}
}

// TestCheckRangeReads only proves the routing table can be read and checked
// unprivileged; whether this PC's range is free is reported, not asserted.
func TestCheckRangeReads(t *testing.T) {
	err := CheckRange()
	if re, ok := err.(*RangeInUseError); ok {
		t.Logf("overlay range in use on this PC: %v", re)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestNLMVtable checks the INetworkConnection slots against the live system:
// every adapter id NLM hands back must be a real interface GUID. A wrong slot
// would return garbage (or crash) here instead of as SYSTEM in the service.
func TestNLMVtable(t *testing.T) {
	var ids []windows.GUID
	err := withCOM(func() error {
		unk, err := oleutil.CreateObject(clsidNetworkListManager)
		if err != nil {
			return err
		}
		defer unk.Release()
		nlm, err := unk.QueryInterface(ole.IID_IDispatch)
		if err != nil {
			return err
		}
		defer nlm.Release()
		v, err := oleutil.CallMethod(nlm, "GetNetworkConnections")
		if err != nil {
			return err
		}
		defer v.Clear()
		return oleutil.ForEach(v.ToIDispatch(), func(item *ole.VARIANT) error {
			defer item.Clear()
			nc, err := item.ToIUnknown().QueryInterface(iidINetworkConnection)
			if err != nil {
				return err
			}
			defer nc.Release()
			vt := (*iNetworkConnectionVtbl)(unsafe.Pointer(nc.RawVTable))
			var g ole.GUID
			if err := hresult(syscallN(vt.GetAdapterID, uintptr(unsafe.Pointer(nc)), uintptr(unsafe.Pointer(&g)))); err != nil {
				return err
			}
			wg, err := windows.GUIDFromString(g.String())
			if err != nil {
				return err
			}
			ids = append(ids, wg)
			var net *ole.IDispatch
			if err := hresult(syscallN(vt.GetNetwork, uintptr(unsafe.Pointer(nc)), uintptr(unsafe.Pointer(&net)))); err != nil {
				return err
			}
			defer net.Release()
			cat, err := getCategory(net)
			if err != nil {
				return err
			}
			if cat < CategoryPublic || cat > CategoryDomain {
				t.Errorf("category %d out of range", cat)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Skip("NLM reports no network connections")
	}
	for _, g := range ids {
		if _, err := winipcfg.LUIDFromGUID(&g); err != nil {
			t.Errorf("adapter id %s from NLM is not an interface: %v", g, err)
		}
	}
	if _, found, err := Category(); err != nil || found {
		t.Logf("Toru LAN category lookup: found=%v err=%v", found, err)
	}
}

// TestBuildRule builds both rules without adding them and reads them back,
// which checks the put_Interfaces vtable slot and the SAFEARRAY encoding.
// The real alias does not exist unelevated, so the rule is pinned to an
// interface that does.
func TestBuildRule(t *testing.T) {
	iface := anyInterfaceAlias(t)
	err := withCOM(func() error {
		for _, c := range []struct {
			name  string
			proto int32
			icmp  string
		}{{api.FirewallRuleInbound, fwProtocolAny, ""}, {api.FirewallRuleICMP, fwProtocolICMP4, "8:*"}} {
			rule, err := newRule(c.name, c.proto, c.icmp, iface)
			if err != nil {
				return err
			}
			r, err := readRule(rule)
			rule.Release()
			if err != nil {
				return err
			}
			t.Logf("%+v", *r)
			if r.Name != c.name || !r.Enabled || r.Direction != fwDirIn || r.Action != fwActionAllow || r.Protocol != c.proto {
				t.Errorf("rule %q read back wrong: %+v", c.name, *r)
			}
			if r.Profiles != fwProfilesAll {
				t.Errorf("rule %q profiles %#x", c.name, r.Profiles)
			}
			if !strings.Contains(r.RemoteAddresses, "100.96.0.0") {
				t.Errorf("rule %q remote %q", c.name, r.RemoteAddresses)
			}
			if !r.PinnedTo(iface) {
				t.Errorf("rule %q interfaces %q", c.name, r.Interfaces)
			}
			if c.icmp != "" && r.IcmpTypes != c.icmp {
				t.Errorf("rule %q icmp %q", c.name, r.IcmpTypes)
			}
			if r.ApplicationName != "" {
				t.Errorf("rule %q is pinned to a program: %q", c.name, r.ApplicationName)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func anyInterfaceAlias(t *testing.T) string {
	rows, err := winipcfg.GetAdaptersAddresses(windows.AF_INET, winipcfg.GAAFlagDefault)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.OperStatus == winipcfg.IfOperStatusUp && r.IfType != winipcfg.IfTypeSoftwareLoopback {
			return r.FriendlyName()
		}
	}
	if len(rows) > 0 {
		return rows[0].FriendlyName()
	}
	t.Skip("no interfaces")
	return ""
}
