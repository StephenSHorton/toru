package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/stream"
)

const (
	testCode  = "ABCD-EFGH-JKMN-PQRS"
	otherCode = "QRST-VWXY-Z234-5678"
)

// fakeStream follows StreamService's rules closely enough for the commands:
// a call needs a room, sharing needs the call, leaving the room hangs up.
type fakeStream struct {
	st      stream.State
	creates int
	joined  []string
	golive  int
}

func (f *fakeStream) GetState() stream.State { return f.st }
func (f *fakeStream) CreateRoom() (stream.State, error) {
	f.creates++
	f.st.Room = &stream.RoomState{Code: otherCode}
	return f.st, nil
}
func (f *fakeStream) JoinRoom(code string) (stream.State, error) {
	c, ok := stream.NormalizeCode(code)
	if !ok {
		return f.st, errors.New("that isn't a room code")
	}
	f.joined = append(f.joined, c)
	f.st.Room = &stream.RoomState{Code: c}
	return f.st, nil
}
func (f *fakeStream) LeaveRoom() stream.State { f.LeaveCall(); f.st.Room = nil; return f.st }
func (f *fakeStream) JoinCall() (stream.State, error) {
	if f.st.Room == nil {
		return f.st, errors.New("join or create a room first")
	}
	f.st.Call.InCall = true
	return f.st, nil
}
func (f *fakeStream) LeaveCall() stream.State { f.StopLive(); f.st.Call.InCall = false; return f.st }
func (f *fakeStream) SetMuted(on bool) stream.State {
	f.st.Call.Muted = on
	if !on {
		f.st.Call.Deafened = false
	}
	return f.st
}
func (f *fakeStream) SetDeafened(on bool) stream.State { f.st.Call.Deafened = on; return f.st }
func (f *fakeStream) GoLive() (stream.State, error) {
	if !f.st.Call.InCall {
		return f.st, errors.New("join the call to share your screen")
	}
	f.golive++
	f.st.Live.Live = true
	return f.st, nil
}
func (f *fakeStream) StopLive() stream.State          { f.st.Live.Live = false; return f.st }
func (f *fakeStream) SetPreview(on bool) stream.State { f.st.Preview = on; return f.st }

type fakeLan struct {
	st       stream.LanState
	enabled  int
	disabled int
	err      error
}

func (f *fakeLan) GetState() stream.LanState { return f.st }
func (f *fakeLan) Enable() (stream.LanState, error) {
	if f.err != nil {
		return f.st, f.err
	}
	f.enabled++
	f.st.Wanted, f.st.State = true, stream.LanStarting
	return f.st, nil
}
func (f *fakeLan) Disable() stream.LanState {
	f.disabled++
	f.st.Wanted, f.st.State = false, stream.LanOff
	return f.st
}

func newStreamReg(s *fakeStream, l *fakeLan) *agentctl.Registry {
	reg := agentctl.NewRegistry()
	registerStreamAgentCommands(reg, s, l)
	return reg
}

// call runs a command and returns its result as JSON text, as the pipe
// would send it.
func call(t *testing.T, reg *agentctl.Registry, name, input string) (string, error) {
	t.Helper()
	if input == "" {
		input = "{}"
	}
	res, err := reg.Call(context.Background(), name, json.RawMessage(input))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	return string(b), nil
}

func TestStreamCommandsRegistered(t *testing.T) {
	reg := newStreamReg(&fakeStream{}, &fakeLan{})
	want := []string{"call.status", "call.start", "call.leave", "call.mute", "room.leave", "share.start", "share.stop", "lan.status", "lan.set", "lan.peers"}
	have := map[string]agentctl.Command{}
	for _, c := range reg.List() {
		have[c.Name] = c
	}
	for _, n := range want {
		if _, ok := have[n]; !ok {
			t.Errorf("%s isn't registered", n)
		}
	}
	if !have["room.leave"].Destructive {
		t.Error("room.leave forgets the room code: it must be destructive")
	}
}

func TestRoomCodeHiddenUnlessAsked(t *testing.T) {
	s := &fakeStream{st: stream.State{Room: &stream.RoomState{Code: testCode}}}
	reg := newStreamReg(s, &fakeLan{})
	for _, c := range []struct{ name, in string }{
		{"call.status", ""}, {"call.start", ""}, {"call.mute", ""},
		{"share.start", ""}, {"share.stop", ""}, {"call.leave", ""}, {"lan.peers", ""},
	} {
		out, err := call(t, reg, c.name, c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if strings.Contains(out, testCode) {
			t.Errorf("%s leaked the room code: %s", c.name, out)
		}
	}
	out, err := call(t, reg, "call.status", `{"showCode":true}`)
	if err != nil || !strings.Contains(out, testCode) {
		t.Errorf("call.status showCode: want the code, got %s (%v)", out, err)
	}
}

func TestCallStart(t *testing.T) {
	// Not in a room and nothing given: an error, no room made.
	s := &fakeStream{}
	reg := newStreamReg(s, &fakeLan{})
	if _, err := call(t, reg, "call.start", ""); err == nil || s.creates != 0 {
		t.Fatalf("call.start outside a room: err %v, creates %d", err, s.creates)
	}
	// new: a room is made, the call joined and the new code shown.
	out, err := call(t, reg, "call.start", `{"new":true}`)
	if err != nil || !s.st.Call.InCall || !strings.Contains(out, otherCode) {
		t.Fatalf("call.start new: %s, %v, inCall %v", out, err, s.st.Call.InCall)
	}

	// In a room: joining its call needs nothing more.
	s = &fakeStream{st: stream.State{Room: &stream.RoomState{Code: testCode}}}
	reg = newStreamReg(s, &fakeLan{})
	if _, err := call(t, reg, "call.start", ""); err != nil || !s.st.Call.InCall {
		t.Fatalf("call.start in a room: %v", err)
	}
	// The same room's code (any spelling) is fine without confirm.
	if _, err := call(t, reg, "call.start", `{"code":"abcd efgh jkmn pqrs"}`); err != nil {
		t.Fatalf("call.start same code: %v", err)
	}
	// Another room, or a new one, forgets this one's code: confirm first.
	for _, in := range []string{`{"code":"` + otherCode + `"}`, `{"new":true}`} {
		if _, err := call(t, reg, "call.start", in); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Errorf("call.start %s without confirm: %v", in, err)
		}
	}
	if s.st.Room.Code != testCode || s.creates != 0 {
		t.Fatalf("room changed without confirm: %s, creates %d", s.st.Room.Code, s.creates)
	}
	if _, err := call(t, reg, "call.start", `{"code":"`+otherCode+`","confirm":true}`); err != nil || s.st.Room.Code != otherCode {
		t.Fatalf("call.start other code with confirm: %v, room %s", err, s.st.Room.Code)
	}
	if _, err := call(t, reg, "call.start", `{"code":"x","new":true}`); err == nil {
		t.Error("code and new together should fail")
	}
	if _, err := call(t, reg, "call.start", `{"code":"nope","confirm":true}`); err == nil {
		t.Error("a bad code should fail")
	}
}

func TestShareOnlyInCall(t *testing.T) {
	s := &fakeStream{st: stream.State{Room: &stream.RoomState{Code: testCode}}}
	reg := newStreamReg(s, &fakeLan{})
	if _, err := call(t, reg, "share.start", ""); err == nil || s.golive != 0 {
		t.Fatalf("share.start outside the call: err %v, golive %d", err, s.golive)
	}
	if _, err := call(t, reg, "call.start", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, reg, "share.start", ""); err != nil || !s.st.Live.Live {
		t.Fatalf("share.start in the call: %v", err)
	}
	if s.st.Preview {
		t.Error("share.start turned the self-preview on without being asked")
	}
	if _, err := call(t, reg, "share.start", `{"preview":true}`); err != nil || !s.st.Preview {
		t.Fatalf("share.start preview: %v", err)
	}
	if _, err := call(t, reg, "share.stop", ""); err != nil || s.st.Live.Live || !s.st.Call.InCall {
		t.Fatalf("share.stop: %v, live %v, inCall %v", err, s.st.Live.Live, s.st.Call.InCall)
	}
}

func TestMuteAndLeave(t *testing.T) {
	s := &fakeStream{st: stream.State{Room: &stream.RoomState{Code: testCode}}}
	reg := newStreamReg(s, &fakeLan{})
	if _, err := call(t, reg, "call.mute", ""); err != nil || !s.st.Call.Muted {
		t.Fatalf("call.mute default: %v", err)
	}
	if _, err := call(t, reg, "call.mute", `{"deafened":true}`); err != nil || !s.st.Call.Deafened {
		t.Fatalf("call.mute deafen: %v", err)
	}
	if _, err := call(t, reg, "call.mute", `{"muted":false}`); err != nil || s.st.Call.Muted || s.st.Call.Deafened {
		t.Fatalf("call.mute unmute: %v", err)
	}
	if _, err := call(t, reg, "room.leave", ""); err == nil || s.st.Room == nil {
		t.Fatalf("room.leave without confirm: %v", err)
	}
	if _, err := call(t, reg, "room.leave", `{"confirm":true}`); err != nil || s.st.Room != nil {
		t.Fatalf("room.leave: %v", err)
	}
}

func TestLanCommands(t *testing.T) {
	s := &fakeStream{st: stream.State{Room: &stream.RoomState{Code: testCode, Members: []stream.Member{
		{ID: "b", Name: "Bea", Lan: &stream.LanInfo{IP: "10.77.0.2", On: true}},
		{ID: "c", Name: "Cy"},
	}}}}
	l := &fakeLan{st: stream.LanState{State: stream.LanOn, IP: "10.77.0.1", Peers: map[string]stream.LanPeer{
		"b": {IP: "10.77.0.2", State: "connected", Path: "direct", RTTMs: 12},
		"z": {IP: "10.77.0.9", State: "connecting"},
	}}}
	reg := newStreamReg(s, l)

	out, err := call(t, reg, "lan.status", "")
	if err != nil || !strings.Contains(out, `"ip":"10.77.0.1"`) {
		t.Fatalf("lan.status: %s %v", out, err)
	}
	if _, err := call(t, reg, "lan.set", ""); err == nil {
		t.Error("lan.set without on should fail")
	}
	if _, err := call(t, reg, "lan.set", `{"on":true}`); err != nil || l.enabled != 1 {
		t.Fatalf("lan.set on: %v", err)
	}
	if _, err := call(t, reg, "lan.set", `{"on":false}`); err != nil || l.disabled != 1 {
		t.Fatalf("lan.set off: %v", err)
	}
	l.err = errors.New("join a room first")
	if _, err := call(t, reg, "lan.set", `{"on":true}`); err == nil {
		t.Error("lan.set on should pass Enable's error on")
	}

	res, err := reg.Call(context.Background(), "lan.peers", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	var got struct {
		Peers []struct {
			ID, Name, IP, State, Path string
			RTTMs                     int
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Peers) != 3 {
		t.Fatalf("lan.peers: want Bea, Cy and the held tunnel z, got %s", b)
	}
	if p := got.Peers[0]; p.Name != "Bea" || p.State != "connected" || p.Path != "direct" || p.RTTMs != 12 {
		t.Errorf("Bea: %+v", p)
	}
	if p := got.Peers[2]; p.ID != "z" || p.State != "connecting" {
		t.Errorf("held tunnel: %+v", p)
	}
}
