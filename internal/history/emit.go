package history

import "github.com/wailsapp/wails/v3/pkg/application"

// EventHistoryChanged is broadcast Go→JS when the recent-captures list mutates
// so an open dashboard Library can refresh without polling.
const EventHistoryChanged = "history:changed"

func emitHistoryChangedImpl() {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit(EventHistoryChanged, nil)
}
