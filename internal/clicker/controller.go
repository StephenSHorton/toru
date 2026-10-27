package clicker

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/clicker/safety"
)

// statusInterval throttles status events and indicator redraws while a run is
// counting down, clicking or paused. State changes always emit at once.
const statusInterval = 100 * time.Millisecond

// shutdownWait is how long Shutdown and Stop wait for a run goroutine to
// release held input and return.
const shutdownWait = 250 * time.Millisecond

// escVK is Escape, which cancels a countdown.
const escVK = 0x1B

// runSource says what started a run: it picks the stop code for a later toggle
// and whether a countdown runs.
type runSource uint8

const (
	srcPage runSource = iota + 1 // Start(): countdown
	srcToggle
	srcHotkey
	srcTray
	srcZone
)

// run is one start-to-stop cycle (countdown included). Fields under "mu" are
// guarded by ClickerService.mu; the atomics are written from the run goroutine.
type run struct {
	gen       uint64
	source    runSource
	startZone int // >= 0 when a start zone started it
	cfg       engine.Config
	safe      safety.Config
	toastOn   bool
	done      chan struct{} // closed when the run goroutine has returned

	// mu
	clk        engine.Clock // nil until the run goroutine has a clock
	stopReq    StopCode     // first stop request, "" = none
	cancel     chan struct{}
	startedAt  time.Time
	pausedAt   time.Time // zero while not paused
	pausedFor  time.Duration
	userPause  bool
	zonePause  bool
	warnSetup  string // BeginRun / clock fallback / allow list
	warnGuard  string // the guard's current warning
	warnInject string // SendInput short counts

	// run goroutine -> status ticker
	paused  atomic.Bool // what the engine's paused() reads
	clicks  atomic.Int64
	point   atomic.Int64
	tick    atomic.Int64
	running atomic.Bool // engine.Run in progress
}

// warning is the status-line note, most specific first.
func (r *run) warning() string {
	for _, w := range []string{r.warnGuard, r.warnInject, r.warnSetup} {
		if w != "" {
			return w
		}
	}
	return ""
}

// elapsed is time spent clicking, pauses excluded.
func (r *run) elapsed(now time.Time) time.Duration {
	if r.startedAt.IsZero() {
		return 0
	}
	d := now.Sub(r.startedAt) - r.pausedFor
	if !r.pausedAt.IsZero() {
		d -= now.Sub(r.pausedAt)
	}
	return max(d, 0)
}

// startBlockedLocked is why a run can't start now (nil = it can). Caller
// holds s.mu.
func (s *ClickerService) startBlockedLocked() error {
	switch {
	case s.closed:
		return clickerErr(ErrAlreadyRunning, "Toru is shutting down")
	case s.cur != nil:
		return clickerErr(ErrAlreadyRunning, "The clicker is already running")
	case s.picking:
		return clickerErr(ErrPickActive, "Finish picking on screen first")
	case !s.settings.Armed:
		return clickerErr(ErrDisarmed, "The clicker is disarmed; arm it to start")
	}
	return nil
}

// begin validates and starts a run. Caller must not hold s.mu.
func (s *ClickerService) begin(src runSource, holdVKs []uint16, startZone int) (Status, error) {
	s.mu.Lock()
	if err := s.startBlockedLocked(); err != nil {
		st := s.statusLocked()
		s.mu.Unlock()
		return st, err
	}
	set := s.settings
	keys := s.keys
	s.mu.Unlock()

	caps := s.d.capsLock != nil && s.d.capsLock()
	if err := checkConflicts(set.Profile, keys, caps); err != nil {
		return s.Status(), err
	}
	cfg, err := buildEngineConfig(set.Profile, &s.d, uint64(s.d.now().UnixNano()))
	if err != nil {
		return s.Status(), err
	}

	// Everything checked above may have changed while unlocked: another start,
	// Shutdown, a disarm or a pick. runs.Add happens under the same lock as
	// the closed check, so Shutdown's runs.Wait always counts this run.
	s.mu.Lock()
	if err := s.startBlockedLocked(); err != nil {
		st := s.statusLocked()
		s.mu.Unlock()
		return st, err
	}
	s.gen++
	r := &run{
		gen:       s.gen,
		source:    src,
		startZone: startZone,
		cfg:       cfg,
		safe:      buildSafetyConfig(set, holdVKs, startZone),
		toastOn:   set.ToastOnStop,
		done:      make(chan struct{}),
		cancel:    make(chan struct{}),
		warnSetup: startWarning(set),
	}
	s.cur = r
	s.last = Status{}
	delay := 0
	if src == srcPage {
		delay = set.StartDelayMs
	}
	if delay > 0 {
		s.state = StateCountdown
		s.countdownEnd = s.d.now().Add(time.Duration(delay) * time.Millisecond)
	} else {
		s.state = StateRunning
		r.startedAt = s.d.now()
	}
	st := s.statusLocked()
	s.runs.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.runs.Done()
		if delay > 0 {
			s.countdown(r)
		} else {
			s.execute(r)
		}
	}()
	s.publish(true)
	s.changed()
	go s.ticker(r)
	return st, nil
}

// countdown waits out StartDelayMs (Esc or Stop cancels), then starts clicking.
func (s *ClickerService) countdown(r *run) {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-r.cancel:
			close(r.done)
			return
		case <-t.C:
		}
		if s.d.physicallyDown != nil && s.d.physicallyDown(escVK) {
			s.stopRun(r.gen, StopUser)
			continue // r.cancel is closed now
		}
		s.mu.Lock()
		if s.cur != r || r.stopReq != "" {
			s.mu.Unlock()
			continue
		}
		if s.d.now().Before(s.countdownEnd) {
			s.mu.Unlock()
			continue
		}
		s.state = StateRunning
		r.startedAt = s.d.now()
		s.mu.Unlock()
		s.publish(true)
		s.changed()
		s.execute(r)
		return
	}
}

// execute owns one run's engine call on a locked thread (BeginRun) and reports
// the end. It always closes r.done.
func (s *ClickerService) execute(r *run) {
	defer close(r.done)
	verdict := s.drive(r)
	s.finish(r, verdict)
}

func (s *ClickerService) drive(r *run) engine.Verdict {
	endRun, warn := func() {}, ""
	if s.d.beginRun != nil {
		endRun, warn = s.d.beginRun()
	}
	defer endRun()

	clk, closeClk, err := s.d.newClock()
	if err != nil || clk == nil {
		if closeClk != nil {
			closeClk()
		}
		clk, closeClk = s.d.newFallbackClk()
		if warn == "" {
			warn = "Low-precision timer: clicks may be less evenly spaced"
		}
	}
	if closeClk != nil {
		defer closeClk()
	}
	inj := s.d.newInjector()
	if clk == nil || inj == nil {
		return engine.Verdict{Kind: engine.Stop, Code: string(StopError), Reason: "The clicker could not start its timer or input"}
	}
	var sysq safety.System
	if s.d.newSystem != nil {
		sysq = s.d.newSystem()
	}
	guard := s.d.newGuard(r.safe, sysq)

	s.mu.Lock()
	r.clk = clk
	if warn != "" {
		r.warnSetup = warn
	}
	stopped := r.stopReq != ""
	// A pause that landed before the clock existed could not interrupt it;
	// deliver it now so the engine pauses on its first wait.
	if r.paused.Load() {
		clk.Interrupt()
	}
	s.mu.Unlock()
	if stopped {
		return engine.Verdict{Kind: engine.Stop, Code: engine.CodeInterrupted}
	}

	shift := r.cfg.WantUpper || r.cfg.WantLower
	s.injecting(true, shift)
	defer s.injecting(false, shift)
	r.running.Store(true)
	defer r.running.Store(false)
	return s.d.run(r.cfg, &watchInjector{inner: inj, s: s, r: r}, clk,
		&watchGuard{inner: guard, s: s, r: r}, r.paused.Load,
		func(p engine.Progress) {
			r.clicks.Store(p.Clicks)
			r.point.Store(int64(p.ActivePoint))
			r.tick.Store(int64(p.PointTick))
		})
}

// injecting reference-counts hotkey.SetInjecting (and SetShiftWrapping, for
// runs that may inject Shift) so a stale run winding down cannot clear the
// flags under a newer one. The flag is applied under s.mu so
// the last count computed is the last one applied (hotkey.SetInjecting only
// stores atomics and posts a message to the hook thread; it never calls back).
func (s *ClickerService) injecting(on, shift bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := func(n *int) {
		if on {
			*n++
		} else if *n > 0 {
			*n--
		}
	}
	step(&s.injectRefs)
	if shift {
		step(&s.shiftRefs)
	}
	if s.d.setInjecting != nil {
		s.d.setInjecting(s.injectRefs > 0)
	}
	if s.d.setShiftWrapping != nil {
		s.d.setShiftWrapping(s.shiftRefs > 0)
	}
}

// finish records the end of a run: lifetime stats always, the status only if
// this run is still the current one (a stopped run may outlive its stop).
func (s *ClickerService) finish(r *run, v engine.Verdict) {
	now := s.d.now()
	s.mu.Lock()
	clicks := r.clicks.Load()
	secs := r.elapsed(now).Seconds()
	if !r.startedAt.IsZero() {
		s.stats.Clicks += clicks
		s.stats.Seconds += secs
		s.stats.Runs++
	}
	stats := s.stats
	current := s.cur == r
	code, reason := StopCode(v.Code), v.Reason
	if r.stopReq != "" && (v.Code == engine.CodeInterrupted || v.Code == "") {
		code, reason = r.stopReq, ""
	}
	if code == "" {
		code = StopError
	}
	if reason == "" {
		reason = stopText(code, r)
	}
	if current {
		s.cur = nil
		s.state = StateIdle
		s.last = Status{Clicks: clicks, ElapsedMs: int64(secs * 1000), StopCode: code, StopReason: reason}
	}
	s.mu.Unlock()

	if err := saveStats(stats); err != nil {
		log.Printf("clicker: save stats: %v", err)
	}
	if !current {
		return
	}
	if code == StopError {
		log.Printf("clicker: run ended with an error: %s", reason)
	}
	s.publish(true)
	s.changed()
	if r.toastOn && toastWorthy(code) && s.d.toast != nil {
		s.d.toast("Auto clicker stopped", reason, code == StopClickLimit || code == StopTimeLimit || code == StopPointsDone)
	}
}

// stopRun asks run gen to stop for code and detaches it at once: the page
// sees idle immediately and a new run may start while the old goroutine
// releases its input. Returns the status after the stop.
func (s *ClickerService) stopRun(gen uint64, code StopCode) (Status, *run) {
	s.mu.Lock()
	r := s.cur
	if r == nil || (gen != 0 && r.gen != gen) {
		st := s.statusLocked()
		s.mu.Unlock()
		return st, nil
	}
	r.stopReq = code
	close(r.cancel)
	r.paused.Store(false)
	if r.clk != nil {
		r.clk.Interrupt()
	}
	now := s.d.now()
	clicks := r.clicks.Load()
	s.cur = nil
	s.state = StateIdle
	s.last = Status{Clicks: clicks, ElapsedMs: r.elapsed(now).Milliseconds(), StopCode: code, StopReason: stopText(code, r)}
	st := s.statusLocked()
	s.mu.Unlock()
	s.publish(true)
	s.changed()
	return st, r
}

// stopAndWait stops the current run and waits up to d for it to let go of
// held input.
func (s *ClickerService) stopAndWait(code StopCode, d time.Duration) Status {
	st, r := s.stopRun(0, code)
	if r != nil {
		select {
		case <-r.done:
		case <-time.After(d):
		}
	}
	return st
}

// setPaused pauses or resumes the current run for the user.
func (s *ClickerService) setPaused(paused bool) Status {
	s.mu.Lock()
	r := s.cur
	if r == nil || s.state == StateCountdown || r.userPause == paused {
		st := s.statusLocked()
		s.mu.Unlock()
		return st
	}
	now := s.d.now()
	r.userPause = paused
	if paused {
		r.paused.Store(true)
		if r.clk != nil {
			r.clk.Interrupt()
		}
	} else {
		// Clear first so the engine's next wait is not mistaken for a stop.
		if r.clk != nil {
			r.clk.ClearInterrupt()
		}
		r.paused.Store(false)
	}
	s.pauseClockLocked(r, now)
	st := s.statusLocked()
	s.mu.Unlock()
	s.publish(true)
	s.changed()
	return st
}

// pauseClockLocked starts or ends the paused-time span after userPause or
// zonePause changed, and sets the state to match.
func (s *ClickerService) pauseClockLocked(r *run, now time.Time) {
	paused := r.userPause || r.zonePause
	switch {
	case paused && r.pausedAt.IsZero():
		r.pausedAt = now
	case !paused && !r.pausedAt.IsZero():
		r.pausedFor += now.Sub(r.pausedAt)
		r.pausedAt = time.Time{}
	}
	if paused {
		s.state = StatePaused
	} else {
		s.state = StateRunning
	}
}

// ticker emits throttled status and redraws the indicator while r is current.
func (s *ClickerService) ticker(r *run) {
	t := time.NewTicker(statusInterval)
	defer t.Stop()
	for {
		select {
		case <-r.cancel:
			return
		case <-r.done:
			return
		case <-t.C:
		}
		s.mu.Lock()
		current := s.cur == r
		s.mu.Unlock()
		if !current {
			return
		}
		s.publish(false)
	}
}

// statusLocked builds the live Status. Caller holds s.mu.
func (s *ClickerService) statusLocked() Status {
	st := s.last
	st.State = s.state
	st.ActivePoint = -1
	st.Armed = s.settings.Armed
	if s.keys != nil {
		if b, ok := s.keys.Binding(actionToggle); ok {
			st.Hotkey = bindingLabel(b)
		}
	}
	r := s.cur
	if r == nil {
		st.State = StateIdle
		return st
	}
	now := s.d.now()
	st.StopCode, st.StopReason = StopNone, ""
	st.Warning = r.warning()
	if s.state == StateCountdown {
		st.CountdownMs = int(max(s.countdownEnd.Sub(now), 0).Milliseconds())
		st.Clicks, st.ElapsedMs = 0, 0
		return st
	}
	st.Clicks = r.clicks.Load()
	st.ElapsedMs = r.elapsed(now).Milliseconds()
	if len(r.cfg.Points) > 0 {
		st.ActivePoint = int(r.point.Load())
		st.PointTick = int(r.tick.Load())
	}
	switch {
	case r.userPause:
		st.PauseCause = PauseUser
	case r.zonePause:
		st.PauseCause = PauseZone
	}
	return st
}

// publish emits the live status (forced, or at most every statusInterval) and
// keeps the indicator pill in step. It returns the status it took.
//
// Publishes are serialized by pubMu and each one snapshots the status under
// s.mu while holding it, so emits and pill updates land in the order the
// snapshots were taken: a ticker publish racing a stop can't re-show the
// pill or a running state after the stop's idle publish. Lock order is
// pubMu, then s.mu; callers must hold neither.
func (s *ClickerService) publish(force bool) Status {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	now := s.d.now()
	s.mu.Lock()
	st := s.statusLocked()
	if !force && now.Sub(s.lastEmit) < statusInterval {
		s.mu.Unlock()
		return st
	}
	s.lastEmit = now
	showPill := s.settings.ShowIndicator
	text := indicatorText(st)
	same := text == s.lastPill
	s.lastPill = text
	emit := s.emit
	s.mu.Unlock()

	if emit != nil {
		emit(EventStatus, st)
	}
	switch {
	case text == "" || !showPill:
		if s.d.indicatorHide != nil && (!same || force) {
			s.d.indicatorHide()
		}
	case !same:
		if s.d.indicatorShow != nil {
			s.d.indicatorShow(text)
		}
	}
	return st
}

// changed tells the host (tray) that the run state or arming changed.
func (s *ClickerService) changed() {
	s.mu.Lock()
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// indicatorText is the running pill: "Clicking · 1,234 · Ctrl+Shift+Y to stop",
// "Paused · 1,234", "Starting in 3". "" hides it.
func indicatorText(st Status) string {
	key := strings.ReplaceAll(st.Hotkey, " + ", "+")
	switch st.State {
	case StateCountdown:
		return fmt.Sprintf("Starting in %d", int(math.Ceil(float64(st.CountdownMs)/1000)))
	case StateRunning:
		if key != "" {
			return "Clicking · " + groupDigits(st.Clicks) + " · " + key + " to stop"
		}
		return "Clicking · " + groupDigits(st.Clicks)
	case StatePaused:
		return "Paused · " + groupDigits(st.Clicks)
	}
	return ""
}

// groupDigits formats n with thousands separators: 1234567 -> "1,234,567".
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// toastWorthy: the stops a user did not ask for (failsafes, limits, errors).
func toastWorthy(c StopCode) bool {
	switch c {
	case StopCorner, StopEdge, StopInZone, StopTaskSwitcher, StopProcessList,
		StopClickLimit, StopTimeLimit, StopPointsDone, StopError:
		return true
	}
	return false
}

// stopText is the display reason for a stop code when the engine or guard did
// not supply one.
func stopText(c StopCode, r *run) string {
	switch c {
	case StopUser:
		return "Stopped"
	case StopHotkey:
		return "Stopped from hotkey"
	case StopHoldRelease:
		return "Hotkey released"
	case StopTray:
		return "Stopped from tray"
	case StopDisarmed:
		return "Clicker disarmed"
	case StopOverlay:
		return "Capture overlay opened"
	case StopKeyCapture:
		return "Recording a shortcut"
	case StopPick:
		return "Picking on screen"
	case StopCorner:
		return "Corner failsafe"
	case StopEdge:
		return "Edge failsafe"
	case StopInZone:
		return "Stop zone failsafe"
	case StopZoneLeft:
		return "Left start zone"
	case StopTaskSwitcher:
		return "Blocked by Alt+Tab"
	case StopProcessList:
		return "Blocked by process list"
	case StopClickLimit:
		if r != nil && r.cfg.ClickLimit > 0 {
			return "Click limit reached (" + groupDigits(r.cfg.ClickLimit) + ")"
		}
		return "Click limit reached"
	case StopTimeLimit:
		if r != nil && r.cfg.TimeLimitNs > 0 {
			return "Time limit reached (" + strconv.FormatFloat(float64(r.cfg.TimeLimitNs)/1e9, 'f', -1, 64) + "s)"
		}
		return "Time limit reached"
	case StopPointsDone:
		return "All click points completed"
	case StopShutdown:
		return "Toru closed"
	}
	return "The clicker stopped unexpectedly"
}

// watchGuard passes the guard's verdicts through and reports zone pauses and
// warnings to the service. Only the run goroutine calls it.
type watchGuard struct {
	inner    safety.Guard
	s        *ClickerService
	r        *run
	paused   bool
	lastWarn string
}

func (g *watchGuard) Check(now int64, clicks int64) engine.Verdict {
	v := g.inner.Check(now, clicks)
	if zp := v.Kind == engine.Pause; zp != g.paused {
		g.paused = zp
		g.s.zonePaused(g.r, zp)
	}
	if w := g.inner.Warning(); w != g.lastWarn {
		g.lastWarn = w
		g.s.mu.Lock()
		g.r.warnGuard = w
		g.s.mu.Unlock()
	}
	return v
}

// zonePaused records a pause zone holding (or releasing) run r.
func (s *ClickerService) zonePaused(r *run, on bool) {
	s.mu.Lock()
	if s.cur != r {
		s.mu.Unlock()
		return
	}
	r.zonePause = on
	s.pauseClockLocked(r, s.d.now())
	s.mu.Unlock()
	s.publish(true)
}

// watchInjector notes when Windows drops some of a run's input (UIPI: an
// elevated window in front, or a secure desktop).
type watchInjector struct {
	inner engine.Injector
	s     *ClickerService
	r     *run
	noted atomic.Bool
}

const blockedWarning = "Windows blocked some clicks: the app in front may be running as administrator"

func (w *watchInjector) Send(ops []engine.Op) (int, error) {
	n, err := w.inner.Send(ops)
	if n < len(ops) && !w.noted.Swap(true) {
		w.s.mu.Lock()
		w.r.warnInject = blockedWarning
		w.s.mu.Unlock()
	}
	return n, err
}

func (w *watchInjector) Teleport(x, y int32) error    { return w.inner.Teleport(x, y) }
func (w *watchInjector) GlideStep(x, y int32) error   { return w.inner.GlideStep(x, y) }
func (w *watchInjector) Cursor() (int32, int32, bool) { return w.inner.Cursor() }
