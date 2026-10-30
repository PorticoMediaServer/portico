import {invalidState, record, seconds} from './validation.ts';
export type SegmentMarkerKind = 'intro' | 'recap' | 'credits' | 'commercial' | 'outro';
export type SegmentMarker = Readonly<{id: string; kind: SegmentMarkerKind; startSeconds: number; endSeconds: number; automaticSafe: boolean}>;
/** Copied marker invariants: bounded, ordered, unique, finite, and within the source clock.
 * Adapters convert the existing wire millisecond fields before entering this seconds-only core. */
export function parseSegmentMarkers(raw: unknown, duration?: number): readonly SegmentMarker[] {
  if (!Array.isArray(raw) || raw.length > 512 || duration !== undefined && (!seconds(duration) || duration === 0)) invalidState();
  const seen = new Set<string>(); let previous = -1;
  return Object.freeze(raw.map(v => {
    if (!record(v) || Object.keys(v).some(k => !['id','kind','startSeconds','endSeconds','automaticSafe'].includes(k)) || typeof v.id !== 'string' || !/^[A-Za-z0-9_-]{1,256}$/.test(v.id) || seen.has(v.id) || !['intro','recap','credits','commercial','outro'].includes(String(v.kind)) || !seconds(v.startSeconds) || !seconds(v.endSeconds) || v.endSeconds <= v.startSeconds || v.startSeconds < previous || typeof v.automaticSafe !== 'boolean' || duration !== undefined && v.endSeconds > duration + 0.001) invalidState();
    seen.add(v.id); previous = v.startSeconds;
    return Object.freeze({id:v.id,kind:v.kind as SegmentMarkerKind,startSeconds:v.startSeconds,endSeconds:v.endSeconds,automaticSafe:v.automaticSafe});
  }));
}
