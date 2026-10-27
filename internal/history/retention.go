package history

import "time"

// Retention is the user's library auto-delete choice. Off keeps every capture
// until the user deletes it; on, a capture goes once it falls past KeepCount
// (newest first) or is older than KeepDays. A zero limit means "no limit" on
// that axis.
type Retention struct {
	AutoDelete bool `json:"autoDelete"`
	KeepCount  int  `json:"keepCount"`
	KeepDays   int  `json:"keepDays"`
}

// normalized clamps negative limits to "no limit".
func (r Retention) normalized() Retention {
	if r.KeepCount < 0 {
		r.KeepCount = 0
	}
	if r.KeepDays < 0 {
		r.KeepDays = 0
	}
	return r
}

// splitExpired partitions items (newest first) into the ones r keeps and the
// ones it deletes as of now.
func splitExpired(items []Item, r Retention, now time.Time) (keep, drop []Item) {
	r = r.normalized()
	if !r.AutoDelete {
		return items, nil
	}
	cutoff := time.Time{}
	if r.KeepDays > 0 {
		cutoff = now.Add(-time.Duration(r.KeepDays) * 24 * time.Hour)
	}
	for i, it := range items {
		tooMany := r.KeepCount > 0 && i >= r.KeepCount
		tooOld := !cutoff.IsZero() && it.TakenAt.Before(cutoff)
		if tooMany || tooOld {
			drop = append(drop, it)
		} else {
			keep = append(keep, it)
		}
	}
	return keep, drop
}
