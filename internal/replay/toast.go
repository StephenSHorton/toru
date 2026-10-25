package replay

import "github.com/StephenSHorton/toru/internal/toast"

// showToast shows the shared native toast (internal/toast) in the top-right
// corner of the active monitor. Never blocks the caller.
func showToast(title, body string, ok bool) { toast.Show(title, body, ok) }
