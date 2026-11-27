package boardshare

import (
	"os"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/stream"
)

// TestTwoStreamServices runs sharing the way Toru wires it (main.go): two
// Torus' own room services, stream.StreamService, each made on an %AppData%
// of its own (its own install id and name), join one room by code, and
// boardshare rides their board messages, which reach it only from members
// heard in presence. TORU_BOARDSHARE_E2E=1 or -args -e2e runs it.
func TestTwoStreamServices(t *testing.T) {
	if os.Getenv("TORU_BOARDSHARE_E2E") == "" && !*e2e {
		t.Skip("set TORU_BOARDSHARE_E2E=1 or pass -args -e2e to run (needs the internet)")
	}
	code := stream.NewCode()
	toru := func(name string) (*board.Store, *ShareService, *stream.StreamService) {
		t.Setenv("APPDATA", t.TempDir())
		svc := stream.NewService()
		set := svc.GetSettings()
		set.Name = name
		if _, err := svc.SetSettings(set); err != nil {
			t.Fatal(err)
		}
		store := board.NewStoreIn(t.TempDir())
		share := New(store)
		store.OnOp(share.OnOp)
		svc.SetBoardHandler(share.Receive)
		share.Attach(svc, nil)
		if _, err := svc.JoinRoom(code); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { share.Stop(); svc.LeaveRoom() })
		return store, share, svc
	}
	aStore, a, aSvc := toru("Stephen")
	bStore, b, bSvc := toru("Blake")
	wait := func(what string, f func() bool) {
		t.Helper()
		deadline := time.Now().Add(40 * time.Second)
		for !f() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	wait("each Toru to see the other in the room", func() bool {
		as, bs := aSvc.GetState(), bSvc.GetState()
		return as.Room != nil && bs.Room != nil && len(as.Room.Members) == 1 && len(bs.Room.Members) == 1
	})
	brd, _ := aStore.Create("Through StreamService")
	if _, err := a.Share(brd.ID); err != nil {
		t.Fatal(err)
	}
	wait("Blake to see the shared board", func() bool { return len(b.GetState().Offered) == 1 })
	if off := b.GetState().Offered[0]; off.HostName != "Stephen" {
		t.Fatalf("offered by %q", off.HostName)
	}
	local, err := b.Join("", brd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bStore.Apply(local, board.Op{Source: board.Person, Blocks: []board.Block{sticky("from Blake", 0)}}); err != nil {
		t.Fatal(err)
	}
	wait("Blake's note on Stephen's board", func() bool {
		x, _ := aStore.Load(brd.ID)
		return len(x.Blocks) == 1 && x.Blocks[0].CreatedBy == board.PersonNamed("Blake")
	})
	if _, err := aStore.Apply(brd.ID, board.Op{Source: board.Person, Blocks: []board.Block{sticky("from Stephen", 300)}}); err != nil {
		t.Fatal(err)
	}
	wait("Stephen's note on Blake's copy", func() bool {
		y, _ := bStore.Load(local)
		return len(y.Blocks) == 2
	})
	y, _ := bStore.Load(local)
	for _, blk := range y.Blocks {
		t.Logf("Blake's copy: %s by %s", blk.Props, blk.CreatedBy)
	}
}
