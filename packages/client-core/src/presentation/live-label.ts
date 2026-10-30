/**
 * MU4 FEAT-07: live timeline label parts. Pure and platform-free.
 *
 * The live label shows the programme's wall-clock range ("7:30 – 8:30 PM");
 * when the viewer is behind live it adds how far ("2:15 behind live"). Time
 * of day comes from the UI's locale formatter (injected, so Hermes and the
 * catalogue stay out of here); durations are plain M:SS / H:MM:SS.
 */

/** A behind-live duration in seconds as "2:15" (or "1:02:15" past an hour). */
export function formatBehindSec(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const h = Math.floor(total / 3600), m = Math.floor((total % 3600) / 60), s = total % 60;
  const mm = h > 0 ? String(m).padStart(2, '0') : String(m);
  return `${h > 0 ? h + ':' : ''}${mm}:${String(s).padStart(2, '0')}`;
}

/** Whether the viewer counts as behind live (mirrors the channel clock's 3 s grace). */
export function isBehindLive(behindSec: number): boolean {
  return Number.isFinite(behindSec) && behindSec > 3;
}

/** Seconds behind live from the channel timeline, clamped at zero. */
export function behindLiveSec(liveEdgeSec: number, positionSec: number): number {
  if (!Number.isFinite(liveEdgeSec) || !Number.isFinite(positionSec)) return 0;
  return Math.max(0, liveEdgeSec - positionSec);
}

/**
 * The programme range label ("7:30 – 8:30 PM"): `format` renders one
 * wall-clock time in the viewer's locale, with or without its meridiem. When
 * the locale uses a meridiem it is shown once, on the end time; locales
 * without one (24-hour clocks) show both times in full. Start/end are
 * programme millis.
 */
export function programmeRangeLabel(startMs: number, endMs: number, format: (ms: number, meridiem: boolean) => string): string {
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) return '';
  const end = format(endMs, true);
  const startFull = format(startMs, true);
  if (startFull === format(startMs, false)) return `${startFull} – ${end}`;
  return `${format(startMs, false)} – ${end}`;
}
