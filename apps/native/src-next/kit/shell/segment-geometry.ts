export type MarkerBounds = Readonly<{startUS: string; endUS: string}>;
export function microseconds(value: string): number | undefined {
  if (!/^(0|[1-9][0-9]*)$/.test(value)) return undefined;
  const result = Number(value);
  return Number.isSafeInteger(result) ? result : undefined;
}
export function parseMarkerTime(value: string): string | undefined {
  const match = /^(?:(\d+):)?(\d+):(\d{2})(?:\.(\d{1,6}))?$/.exec(value.trim());
  if (!match || Number(match[3]) > 59 || (match[1] !== undefined && Number(match[2]) > 59)) return undefined;
  const result = (Number(match[1] ?? 0) * 3600 + Number(match[2]) * 60 + Number(match[3])) * 1e6 + Number((match[4] ?? '').padEnd(6, '0'));
  return Number.isSafeInteger(result) ? String(result) : undefined;
}
export function markerTime(value: string): string {
  const us = microseconds(value);
  if (us === undefined) return '';
  const whole = Math.floor(us / 1e6), minutes = Math.floor(whole / 60), seconds = whole % 60;
  const fraction = String(us % 1e6).padStart(6, '0').replace(/0+$/, '');
  return `${minutes}:${String(seconds).padStart(2, '0')}${fraction ? `.${fraction}` : ''}`;
}
export function validMarker(bounds: MarkerBounds, durationUS: string): boolean {
  const start = microseconds(bounds.startUS), end = microseconds(bounds.endUS), duration = microseconds(durationUS);
  return start !== undefined && end !== undefined && duration !== undefined && start < end && end <= duration;
}
export function moveMarkerEdge(bounds: MarkerBounds, edge: 'start' | 'end', value: number, duration: number): MarkerBounds {
  const currentStart = microseconds(bounds.startUS), currentEnd = microseconds(bounds.endUS);
  if (currentStart === undefined || currentEnd === undefined || !Number.isSafeInteger(duration) || duration < 1 || !Number.isFinite(value)) return bounds;
  return edge === 'start' ? {startUS: String(Math.max(0, Math.min(currentEnd - 1, Math.round(value)))), endUS: bounds.endUS}
    : {startUS: bounds.startUS, endUS: String(Math.max(currentStart + 1, Math.min(duration, Math.round(value))))};
}
export function markerWindowParts(bounds: MarkerBounds, window: MarkerBounds) {
  const start = microseconds(bounds.startUS), end = microseconds(bounds.endUS), from = microseconds(window.startUS), to = microseconds(window.endUS);
  if (start === undefined || end === undefined || from === undefined || to === undefined || end <= start || to <= from || end <= from || start >= to) return undefined;
  return {left: Math.max(0, (start - from) / (to - from)), right: Math.min(1, (end - from) / (to - from))};
}
