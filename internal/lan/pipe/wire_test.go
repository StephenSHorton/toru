package pipe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/lan/api"
)

func TestDecodeAccepts(t *testing.T) {
	good := []string{
		`{"type":"hello","id":1,"hello":{"major":1,"minor":0}}`,
		`{"type":"status","id":2}`,
		`{"type":"down"}`,
		`{"type":"up","up":{"room":"ab12","self":"0123abcd","iceServers":[{"urls":["stun:stun.l.google.com:19302"]},{"urls":["turns:relay.example:443?transport=tcp"],"username":"1700000000:x","credential":"c2VjcmV0"}],"policy":{"allowServices":false,"mdns":false}}}`,
		`{"type":"signal","signal":{"peer":"peer-1","blob":{"kind":"hello","gen":0,"wgPub":"` + strings.Repeat("A", 43) + `=","ip":"100.96.12.34"}}}`,
		`{"type":"signal","signal":{"peer":"peer_1","blob":{"kind":"cand","gen":3,"cand":"candidate:1 1 udp 2130706431 192.168.1.5 50000 typ host"}}}`,
		`{"type":"signal","signal":{"peer":"p","blob":{"kind":"cand","gen":3,"cand":""}}}`,
		`{"type":"drop","drop":{"peer":"p"}}`,
		`{"type":"readdress","readdress":{"salt":7}}`,
		`{"type":"setPolicy","setPolicy":{"allowServices":true,"mdns":true}}`,
		`{"type":"setIce","setIce":{"iceServers":[]}}`,
		// A newer GUI's extra field is ignored, not refused.
		`{"type":"status","future":true}`,
	}
	for _, s := range good {
		if _, _, err := Decode([]byte(s)); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	bigBlob := `{"kind":"cand","cand":"` + strings.Repeat("x", MaxBlob) + `"}`
	var manyICE []api.ICEServer
	for i := 0; i <= MaxICEServers; i++ {
		manyICE = append(manyICE, api.ICEServer{URLs: []string{"stun:a"}})
	}
	manyJSON, _ := json.Marshal(api.Message{Type: api.TypeSetICE, SetICE: &api.SetICE{ICEServers: manyICE}})
	bad := map[string]string{
		"not json":            `{"type":`,
		"unknown type":        `{"type":"route","id":1}`,
		"service-only type":   `{"type":"peerState","peerState":{"peer":"p","state":"connected"}}`,
		"missing payload":     `{"type":"up"}`,
		"two payloads":        `{"type":"drop","drop":{"peer":"p"},"readdress":{"salt":1}}`,
		"down with payload":   `{"type":"down","drop":{"peer":"p"}}`,
		"empty room":          `{"type":"up","up":{"room":"","self":"a","iceServers":[]}}`,
		"bad self":            `{"type":"up","up":{"room":"r","self":"a/b","iceServers":[]}}`,
		"http ICE":            `{"type":"setIce","setIce":{"iceServers":[{"urls":["http://evil"]}]}}`,
		"ICE without urls":    `{"type":"setIce","setIce":{"iceServers":[{"urls":[]}]}}`,
		"ICE control char":    `{"type":"setIce","setIce":{"iceServers":[{"urls":["stun:a\nb"]}]}}`,
		"too many ICE":        string(manyJSON),
		"peer too long":       `{"type":"drop","drop":{"peer":"` + strings.Repeat("a", 65) + `"}}`,
		"peer with space":     `{"type":"drop","drop":{"peer":"a b"}}`,
		"blob not object":     `{"type":"signal","signal":{"peer":"p","blob":"x"}}`,
		"blob missing":        `{"type":"signal","signal":{"peer":"p"}}`,
		"blob kind":           `{"type":"signal","signal":{"peer":"p","blob":{"kind":"route"}}}`,
		"blob too big":        `{"type":"signal","signal":{"peer":"p","blob":` + bigBlob + `}}`,
		"ip outside overlay":  `{"type":"signal","signal":{"peer":"p","blob":{"kind":"hello","ip":"192.168.1.10"}}}`,
		"ip is the broadcast": `{"type":"signal","signal":{"peer":"p","blob":{"kind":"hello","ip":"100.96.255.255"}}}`,
		"ip is the gateway":   `{"type":"signal","signal":{"peer":"p","blob":{"kind":"hello","ip":"100.96.0.1"}}}`,
		"ip is a prefix":      `{"type":"signal","signal":{"peer":"p","blob":{"kind":"hello","ip":"100.96.0.0/16"}}}`,
		"wgPub short":         `{"type":"signal","signal":{"peer":"p","blob":{"kind":"hello","wgPub":"AAAA"}}}`,
		"too long":            `{"type":"status","x":"` + strings.Repeat("x", MaxLine) + `"}`,
	}
	for name, s := range bad {
		_, _, err := Decode([]byte(s))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if ce, ok := err.(*CodeError); !ok || ce.Code != api.ErrBadRequest {
			t.Errorf("%s: error %v is not bad-request", name, err)
		}
	}
}

func TestSignalBlobReturned(t *testing.T) {
	_, blob, err := Decode([]byte(`{"type":"signal","signal":{"peer":"p","blob":{"kind":"creds","gen":2,"ufrag":"u","pwd":"w","direct":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if blob == nil || blob.Kind != api.KindCreds || blob.Gen != 2 || blob.Ufrag != "u" || !blob.Direct {
		t.Fatalf("blob %+v", blob)
	}
}
