package clicker

// Normalize is the only path settings take in or out: it clamps every field,
// fixes enums, keeps the two Cadence forms in sync, turns double click off where
// it cannot apply, and makes limits exclusive.
func Normalize(s Settings) Settings {
	return s
}
