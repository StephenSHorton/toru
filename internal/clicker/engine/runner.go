package engine

// Run clicks until a limit is reached, the guard says Stop, or the caller
// interrupts the clock while paused() reports false. When the clock is
// interrupted and paused() is true the run releases anything held and waits
// (re-checking the guard) until paused() turns false, then resumes without a
// burst of catch-up clicks. It returns the final verdict (Kind == Stop).
// onProgress may be nil.
func Run(cfg Config, inj Injector, clk Clock, g Guard, paused func() bool, onProgress func(Progress)) Verdict {
	return Verdict{Kind: Stop, Code: CodeError, Reason: "engine not built yet"}
}
