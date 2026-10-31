package fx

import (
	"math"
	"strings"
)

// expr.go holds what the time-based builders share: numbers written into
// filter arguments, and ffmpeg expressions in t for the curves the preview
// computes in JS (types/<type>.ts). Both sides compute in doubles, so the
// values agree.

// round4 rounds to 4 decimals, so an argument reads the same as the
// preview's value (the preview rounds the same way).
func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// easeExpr is keyframes.go's ease(kind, x) as an expression of x (an
// expression already clamped to 0..1).
func easeExpr(kind, x string) string {
	switch kind {
	case "in":
		return "pow(" + x + ",3)"
	case "out":
		return "(1-pow(1-" + x + ",3))"
	case "inOut":
		return "if(lt(" + x + ",0.5),4*pow(" + x + ",3),1-pow(2-2*" + x + ",3)/2)"
	}
	return x
}

// quoted wraps an expression for a filter option, so its commas and colons
// stay inside it.
func quoted(e string) string { return "'" + strings.ReplaceAll(e, "'", "") + "'" }
