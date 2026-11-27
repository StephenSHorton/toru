package main

import (
	"testing"
)

// The tray menu has "New whiteboard" (main.go wires it to
// WhiteboardsService.Create, which TestWhiteboardsCreateNamesAndOpens covers).
func TestTrayNewWhiteboard(t *testing.T) {
	tc := &trayController{newWhiteboard: func() {}}
	menu := tc.buildMenu()
	var labels []string
	for i := 0; menu.ItemAt(i) != nil; i++ {
		if l := menu.ItemAt(i).Label(); l != "" {
			labels = append(labels, l)
		}
	}
	t.Logf("tray menu: %q", labels)
	if menu.FindByLabel("New whiteboard") == nil {
		t.Fatalf("no New whiteboard in the tray menu %q", labels)
	}
}
