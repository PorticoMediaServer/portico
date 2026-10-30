import {linkAbortSignals} from './abort-lifetime.ts';
import type {ReadingAnnotation, ReadingAnnotationPort} from './reading-annotations.ts';

export type ReadingHighlightsFile = Readonly<{fileName: string; markdown: string}>;
export type ReadingHighlightsExport = Readonly<{
  port: ReadingAnnotationPort; revision: number; title?: string; author?: string;
  signal: AbortSignal; isCurrent: () => boolean;
}>;
const encoder = new TextEncoder();
const MAX_BYTES = 4 * 1024 * 1024;
// Metadata headings are plain Markdown text; annotation bodies are indented code,
// so quotes/notes cannot introduce links, images, raw HTML or fenced-code escapes.
const heading = (text: string) => text.replace(/[\x00-\x1f\x7f\u202a-\u202e\u2066-\u2069]/g, ' ').replace(/[!"#$%&'()*+,\-./:;<=>?@[\\\]^_`{|}~]/g, '\\$&');
const code = (text: string) => text.replace(/\r\n?/g, '\n').replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\u202a-\u202e\u2066-\u2069]/g, '').split('\n').map(line => `    ${line}`).join('\n');
/** Explicit current-profile/current-publication export. No persistent copy or new authority. */
export async function collectReadingHighlights(options: ReadingHighlightsExport): Promise<ReadingHighlightsFile> {
  const {port, revision, signal, isCurrent} = options;
  let lifetime: ReturnType<typeof linkAbortSignals> | undefined;
  const title = options.title, author = options.author;
  try {
    lifetime = linkAbortSignals([signal, port.signal], 15000);
    const combined = lifetime.signal;
    const check = () => {if (combined.aborted || !isCurrent() || port.manifest.canAnnotate !== true) throw new Error('reading_export_retired');};
    if (!Number.isSafeInteger(revision) || revision < 0 || title !== undefined && encoder.encode(title).length > 4096 || author !== undefined && encoder.encode(author).length > 4096) throw new Error('invalid_reading_export');
    check();
    const entries: ReadingAnnotation[] = [];
    let cursor = '', bytes = 0, complete = false;
    for (let pageIndex = 0; pageIndex < 6; pageIndex++) {
      check();
      const page = await port.annotations(cursor, revision, combined);
      check();
      if (page.revision !== revision || page.sourceId !== port.manifest.sourceId || page.sourceRevision !== port.manifest.revision || page.entries.length > 50) throw new Error('annotation_revision_changed');
      let previous = cursor;
      for (const entry of page.entries) {
        if (entry.id <= previous || entries.length >= 256) throw new Error('invalid_reading_export');
        previous = entry.id;
        bytes += encoder.encode(entry.text).length + encoder.encode(entry.note).length;
        if (bytes > MAX_BYTES) throw new Error('reading_export_too_large');
        entries.push(entry);
      }
      if (!page.nextCursor) {complete = true; break;}
      if (page.entries.length !== 50 || page.nextCursor !== previous) throw new Error('invalid_reading_export');
      cursor = page.nextCursor;
    }
    if (!complete) throw new Error('reading_export_too_large');
    check();
    const highlights = entries.filter(entry => entry.kind === 'highlight').sort((a, b) => a.location.progression - b.location.progression || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    const parts = ['# Highlights', ...(title ? [`## ${heading(title)}`] : []), ...(author ? [heading(author)] : [])];
    for (const entry of highlights) {
      check();
      const location = entry.location;
      parts.push(`### ${location.format !== 'epub' && typeof location.pageIndex === 'number' ? `Page ${location.pageIndex + 1}` : `${Math.round(location.progression * 10000) / 100}%`}`, code(entry.text));
      if (entry.note) parts.push('Note:', code(entry.note));
    }
    const markdown = parts.join('\n\n') + '\n';
    if (encoder.encode(markdown).length > MAX_BYTES) throw new Error('reading_export_too_large');
    // Recheck the exact collection revision after acquisition/rendering. Never
    // silently save the loaded page or a collection that changed between pages.
    check();
    const confirmed = await port.annotations('', revision, combined);
    check();
    if (confirmed.revision !== revision || confirmed.sourceId !== port.manifest.sourceId || confirmed.sourceRevision !== port.manifest.revision) throw new Error('annotation_revision_changed');
    const stem = (title ?? '').normalize('NFKD').replace(/[^A-Za-z0-9 -]/g, '').trim().replace(/\s+/g, '-').slice(0, 64);
    return Object.freeze({fileName: `${stem || 'reading'}-highlights.md`, markdown});
  } finally {lifetime?.dispose();}
}
