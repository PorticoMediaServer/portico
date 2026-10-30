/** Unknown, contradictory or unbounded counters never become a percentage. */
export function counterProgress(processed: number | undefined, total: number | undefined): number | undefined {
  if (processed === undefined || total === undefined || !Number.isFinite(processed) || !Number.isFinite(total) || processed < 0 || total <= 0 || processed > total) return undefined;
  return processed / total;
}
export function zoomedSegmentRange(marks: readonly {start: number; end: number}[], duration: number, endWindow: boolean): {start: number; end: number} | undefined {
  if (!Number.isFinite(duration) || duration <= 0 || marks.length === 0) return undefined;
  const starts = marks.map(mark => mark.start), ends = marks.map(mark => mark.end);
  if (marks.some(mark => !Number.isFinite(mark.start) || !Number.isFinite(mark.end) || mark.start < 0 || mark.end <= mark.start || mark.end > duration)) return undefined;
  const start = Math.max(0, Math.floor((Math.min(...starts) - 120) / 60) * 60);
  const end = endWindow ? duration : Math.min(duration, Math.ceil((Math.max(...ends) + 120) / 60) * 60);
  return end > start ? {start, end} : undefined;
}
export function segmentClock(seconds: number, tenths = false): string {
  const value = Math.max(0, Math.round(seconds * (tenths ? 10 : 1))), minute = Math.floor(value / (tenths ? 600 : 60));
  return tenths ? `${minute}:${((value % 600) / 10).toFixed(1).padStart(4, '0')}` : `${minute}:${String(value % 60).padStart(2, '0')}`;
}
export function readSegmentClock(text: string): number | undefined {
  if (!/^\d+:\d{2}(?:\.\d)?$/.test(text)) return undefined;
  const colon = text.indexOf(':'), minutes = Number(text.slice(0, colon)), seconds = Number(text.slice(colon + 1));
  return Number.isFinite(minutes) && seconds < 60 ? minutes * 60 + seconds : undefined;
}
/** Display the actual profile only when it adds an identity beyond the username. */
export function differingProfileName(profile: string, username?: string): string | undefined {
  return profile.trim() && profile.trim().toLocaleLowerCase() !== username?.trim().toLocaleLowerCase() ? profile : undefined;
}
