import type {PutReadingSessionsSessionIdProgressRequest} from '../../contracts/src/types.ts';

export type ReadingAnnotationLocation = Readonly<PutReadingSessionsSessionIdProgressRequest['location']>;
export type ReadingAnnotationRect = Readonly<{x: number; y: number; width: number; height: number}>;
export type ReadingAnnotationContent = Readonly<{id: string; kind: 'bookmark' | 'highlight'; location: ReadingAnnotationLocation; text: string; note: string; colour: '' | 'yellow' | 'green' | 'blue' | 'pink'; rects?: readonly ReadingAnnotationRect[]}>;
export type ReadingAnnotation = ReadingAnnotationContent & Readonly<{createdAt: number; updatedAt: number}>;
export type ReadingAnnotationsPage = Readonly<{sourceId: string; sourceRevision: string; revision: number; entries: readonly ReadingAnnotation[]; nextCursor: string}>;
export type ReadingAnnotationMutation = Readonly<{expectedRevision: number; operation: 'delete'; id: string}> | (ReadingAnnotationContent & Readonly<{expectedRevision: number; operation: 'upsert'}>);
export type ReadingAnnotationPort = Readonly<{
  manifest: Readonly<{sourceId: string; revision: string; canAnnotate?: true}>; signal: AbortSignal;
  annotations: (cursor?: string, revision?: number, signal?: AbortSignal) => Promise<ReadingAnnotationsPage>;
  mutateAnnotation: (request: ReadingAnnotationMutation, signal?: AbortSignal) => Promise<Readonly<{revision: number}>>;
}>;
const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const integer = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
export const readingAnnotationId = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,64}$/.test(v);
const text = (v: unknown, max: number): v is string => typeof v === 'string' && new TextEncoder().encode(v).length <= max && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
function invalid(): never {throw new Error('invalid_reading_annotations');}
type ValidateLocation = (location: ReadingAnnotationLocation) => void;
function content(v: Record<string, unknown>, validate: ValidateLocation): ReadingAnnotationContent {
  if (!readingAnnotationId(v.id) || v.kind !== 'bookmark' && v.kind !== 'highlight' || !record(v.location) || !text(v.text, 8192) || !text(v.note, 4096)) invalid();
  const l = v.location;
  if (!integer(l.pageIndex) && l.pageIndex !== undefined || typeof l.progression !== 'number' || !Number.isFinite(l.progression) || l.progression < 0 || l.progression > 1) invalid();
  let location: ReadingAnnotationLocation;
  if (l.format === 'epub') {
    if (!text(l.cfi, 4096) || !l.cfi.startsWith('epubcfi(/') || !l.cfi.endsWith(')') || l.pageIndex !== undefined || l.section !== undefined && !text(l.section, 1024)) invalid();
    location = Object.freeze({format: 'epub', cfi: l.cfi, ...(l.section === undefined ? {} : {section: l.section}), progression: l.progression});
  } else {
    if (l.format !== 'pdf' && l.format !== 'cbz' && l.format !== 'cbr' || !integer(l.pageIndex) || l.pageIndex > 1000000 || l.cfi !== undefined || l.section !== undefined) invalid();
    location = Object.freeze({format: l.format, pageIndex: l.pageIndex, progression: l.progression});
  }
  validate(location);
  const rects: ReadingAnnotationRect[] = [];
  if (v.rects !== undefined) {
    if (!Array.isArray(v.rects) || v.rects.length > 128) invalid();
    for (const r of v.rects) {
      if (!record(r) || !['x','y','width','height'].every(key => {const value=r[key];return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1;})
        || (r.width as number) <= 0 || (r.height as number) <= 0 || (r.x as number)+(r.width as number)>1 || (r.y as number)+(r.height as number)>1) invalid();
      rects.push(Object.freeze({x:r.x as number,y:r.y as number,width:r.width as number,height:r.height as number}));
    }
  }
  if (rects.length && (location.format !== 'pdf' || v.kind !== 'highlight') || v.kind === 'highlight' && location.format === 'pdf' && !rects.length) invalid();
  if (typeof v.colour !== 'string') invalid();
  if (v.kind === 'bookmark' ? v.text !== '' || v.colour !== '' : !v.text.trim() || !['yellow', 'green', 'blue', 'pink'].includes(v.colour)) invalid();
  return Object.freeze({id: v.id, kind: v.kind, location, text: v.text, note: v.note, colour: v.colour as ReadingAnnotationContent['colour'], ...(rects.length ? {rects:Object.freeze(rects)} : {})});
}
export function captureReadingAnnotationMutation(raw: unknown, validate: ValidateLocation): ReadingAnnotationMutation {
  if (!record(raw) || !integer(raw.expectedRevision) || raw.expectedRevision === Number.MAX_SAFE_INTEGER || !readingAnnotationId(raw.id)) invalid();
  if (raw.operation === 'delete') return Object.freeze({operation: 'delete', expectedRevision: raw.expectedRevision, id: raw.id});
  if (raw.operation !== 'upsert') invalid();
  return Object.freeze({...content(raw, validate), operation: 'upsert', expectedRevision: raw.expectedRevision});
}
export function parseReadingAnnotations(raw: unknown, source: Readonly<{sourceId: string; revision: string}>, validate: ValidateLocation, cursor = ''): ReadingAnnotationsPage {
  if (!record(raw) || raw.sourceId !== source.sourceId || raw.sourceRevision !== source.revision || !integer(raw.revision) || !Array.isArray(raw.entries) || raw.entries.length > 50 || typeof raw.nextCursor !== 'string' || raw.nextCursor !== '' && !readingAnnotationId(raw.nextCursor)) invalid();
  const entries: ReadingAnnotation[] = []; let previous = cursor;
  for (const row of raw.entries) {
    if (!record(row) || !integer(row.createdAt) || !integer(row.updatedAt) || row.updatedAt < row.createdAt || !Number.isFinite(new Date(row.createdAt).getTime()) || !Number.isFinite(new Date(row.updatedAt).getTime())) invalid();
    const value = content(row, validate); if (value.id <= previous) invalid(); previous = value.id;
    entries.push(Object.freeze({...value, createdAt: row.createdAt, updatedAt: row.updatedAt}));
  }
  if (raw.nextCursor && (entries.length !== 50 || raw.nextCursor !== previous)) invalid();
  return Object.freeze({sourceId: source.sourceId, sourceRevision: source.revision, revision: raw.revision, entries: Object.freeze(entries), nextCursor: raw.nextCursor});
}
export function parseReadingAnnotationSaved(raw: unknown, expected: number): Readonly<{revision: number}> {
  if (!record(raw) || !integer(raw.revision) || raw.revision !== expected + 1) invalid();
  return Object.freeze({revision: raw.revision});
}
