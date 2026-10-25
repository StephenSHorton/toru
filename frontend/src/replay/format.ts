// Formatting shared by the recording settings panel and the Timeline viewer.

// Background recording: how much footage (summed across every game and
// session, not wall-clock age) is kept before the oldest is deleted. Must match
// replay.RetentionOptions on the Go side; 0 = sessions stay until the storage
// limit needs the space.
export const RETENTION_OPTIONS = [0, 1800, 3600, 7200, 14400, 28800, 86400];

// The chips as the settings panel shows them: the durations ascending, then
// "Until storage is full" (0) closing the row.
export const RETENTION_CHIPS = [...RETENTION_OPTIONS.filter((v) => v > 0), 0];

export function fmtRetention(sec: number): string {
  if (sec <= 0) return "Until storage is full";
  if (sec < 3600) return `${Math.round(sec / 60)} min`;
  return `${Math.round(sec / 3600)} h`;
}
