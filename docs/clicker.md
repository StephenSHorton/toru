# Auto Clicker — click or press a key at a set rate

The Auto Clicker clicks a mouse button, or presses a key, over and over at a
rate you choose. It can click wherever the cursor is or walk through a list of
screen points, and it stops on its own when the cursor hits a screen corner or
edge, enters a zone you drew, or the wrong app comes to the front. Settings are
saved as you change them; whole setups can be kept as presets.

Open the hub (tray → **Open Toru…**) and pick **Auto Clicker** in the left nav,
or use **Auto clicker settings…** in the tray menu.

## Starting and stopping

- **Ctrl+Shift+Y** starts and stops the clicker from anywhere. Change it in
  Settings → Shortcuts, or on the Auto Clicker page. Chords, left/right-only
  modifiers, loose matching, a modifier on its own and the mouse buttons
  Middle, Mouse4 and Mouse5 work too (but see
  [Apps running as administrator](#apps-running-as-administrator)).
- **Toggle / Hold**: in Toggle mode a press starts and the next press stops. In
  Hold mode the clicker runs only while you hold the shortcut down.
- **Start** on the page counts down first (3 seconds by default, set in the
  page), so the clicker does not click the Start button itself. **Stop** or
  **Esc** cancels the countdown. The shortcut and the tray start at once.
- **Tray**: the *Auto clicker* item starts and stops it and shows a check while
  it runs.
- **Pause** holds a run without ending it; the counter and elapsed time keep
  their values.
- **Armed**: while the clicker is disarmed its shortcut is handed back to other
  apps (Ctrl+Shift+Y does whatever the app in front does with it) and nothing
  can start a run. Disarming stops a running clicker. An optional *Arm / disarm
  auto clicker* shortcut flips this from anywhere.

While it runs a small pill near the top of the screen shows the click count and
the stop shortcut (turn it off with *Show indicator*). Opening Toru's capture
overlay, recording a shortcut, or picking points on screen stops the clicker
first, so its clicks never land in Toru itself.

## What it presses

- **Rate or interval**: set clicks per second, minute, hour or day, or the time
  between clicks (h / m / s / ms). The two always agree. The fastest rate is
  500 clicks a second, or 1,000 with *Allow up to 1,000 clicks a second*. The
  rate box takes at most that number in any unit (500 a minute, 500 an hour),
  so for more than that per minute or hour use a shorter unit or the interval.
- **Mouse**: left, right, middle, back (Mouse4) or forward (Mouse5).
- **Keyboard**: any key Toru knows, sent the way a real keyboard sends it (by
  scan code), so games that read raw keys see it. Letters can be forced upper
  or lower case; the clicker adds Shift as needed and never touches Caps Lock.
- **Click / Hold**: *Click* presses and releases every cycle and holds for the
  *hold %* of the interval. With the mouse, *Hold* holds the button for the
  interval, lets go, and waits the gap you set. With a key, *Hold* keeps the key
  down and repeats it at Windows' own key-repeat speed (Settings → Keyboard in
  Windows), ignoring the rate.
- **Double click** turns each click into a double click. It is off at 11
  clicks a second and above, and in Hold mode, where it cannot work.
- **Randomize timing** spreads each interval around the set value (the % is
  how wide the spread is), so the rhythm is less mechanical.
- **Limits**: stop after a number of clicks (up to 100,000,000) or after an
  amount of time (up to 1,000 hours).

## Click points

Turn on *Click points* to click a list of screen spots in turn instead of the
cursor position. Each point has a number of clicks before moving on and an
optional radius to scatter the clicks around it. **Pick** puts a crosshair on
screen:

- **Right click** adds a point and finishes; **Shift + right click** adds one
  and keeps picking.
- **Ctrl + right click** removes the nearest point.
- **Esc** ends the pick.

*Stop after one pass* ends the run after the last point; *Glide* moves the
cursor smoothly between points when there is time. A warning sign next to a
point's number means it sits inside a corner or edge failsafe on your current
screens: a run stops as soon as the cursor gets there.

## Failsafes

The clicker stops by itself when:

- the cursor enters a **corner** square or an **edge** band of any screen (sizes
  in px, 0 turns that one off). *Outer edges only* (on by default) ignores the
  edges between two monitors, so moving across screens does not stop it;
- the cursor enters a **stop zone**;
- **Alt+Tab** or Win+Tab opens (*Stop on Alt+Tab*);
- the app in front is not allowed by the **process list**: with *Allow* the
  clicker runs only while one of the checked apps is in front, with *Block* it
  stops when one of them comes to the front;
- a click or time limit is reached, or the click points are done.

When a failsafe or a limit stops the run, a small toast says why (turn off with
*Notify on stop*). *Preview* flashes the corners, edges and zones on screen.
With *Show safety overlay* on (the default) they also flash briefly whenever
you change them, and so do the click points.

### Zones

Draw rectangles on screen (right-drag, Esc cancels) and give each one an action:

- **Stop**: entering it stops the run.
- **Pause**: the clicker waits while the cursor is inside and carries on when it
  leaves.
- **Start**: while the clicker is armed and idle, moving the cursor into a start
  zone starts a run, and leaving the zone stops it again. A start zone inside a
  pause zone keeps clicking there.

When zones overlap, stop beats start and start beats pause.

## Apps running as administrator

Windows does not let a normal program send input into a program running as
administrator. Toru runs as a normal program, so clicks and keys aimed at an
elevated window (some games, launchers, Task Manager) are dropped. The page
shows a warning when that happens.

Toru's keyboard and mouse hooks are not called while an elevated window is in
front either, so shortcuts that only the hooks can see do nothing there:

- A start/stop shortcut that is a chord, uses left/right-only modifiers or
  loose matching, is a modifier on its own, or uses Middle, Mouse4 or Mouse5.
  A plain shortcut like Ctrl+Shift+Y is registered with Windows instead and
  does work, unless another app already owns that combo (Toru then falls back
  to the hook).
- The start/stop shortcut while a run is going: during a run Toru matches it
  with the hook (the run's own Shift presses would confuse Windows' matching),
  so to stop a run over an elevated window use the tray or the page, or move
  the cursor into a failsafe.
- The *Arm / disarm auto clicker* shortcut, whatever it is.
- In Hold mode, letting go of the shortcut over an elevated window is not seen,
  so the run keeps clicking until you press and release the shortcut again
  with a normal window in front, or a failsafe stops it.

## Presets

A preset stores everything about *how* the clicker clicks (rate, input, timing,
limits, click points, failsafes and the process list); app preferences such as
the shortcut, zones, countdown and indicator are not part of it.

- **Save** stores the current setup under a new name (up to 40 characters,
  unique ignoring case) and makes it active.
- **Apply** loads a preset. The page marks it *Active*, or *Modified* once you
  change something; **Update** saves those changes into it.
- **Rename**, **Duplicate** (adds "Name (2)"), **Delete**.
- **Export** writes the preset to a `.json` file; **Import** adds one from a file.
- Three starters come with a fresh install: *Standard Clicking* (25/s),
  *Rapid Fire* (100/s, stops after 5,000) and *Precision Mode* (10/s). Delete
  them freely; they do not come back.

Presets cannot be changed while the clicker runs (export still works).

### Importing from Blur AutoClicker

**Import** also reads preset files exported by Blur AutoClicker. Toru maps
Blur's rate, input, key and case, button, duty cycle and hold mode, speed
randomization (capped at 100 %), double click, limits, corner and edge sizes,
click points, the zones switch and the process list. Blur's start/stop hotkey
and Toggle/Hold mode are not imported (Toru keeps shortcuts in its own
settings), and anything Blur adds later that Toru does not know is ignored.
Missing values are taken from your current setup.

## Files

- `%AppData%\toru\clicker.json`: settings and presets.
- `%AppData%\toru\clicker-stats.json`: lifetime totals (clicks, time, runs).
- The two clicker shortcuts live with the other shortcuts in
  `%AppData%\toru\settings.json`.
