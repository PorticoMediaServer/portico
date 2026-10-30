import type {ReadingClient} from '../../../../../packages/client-core/src';

const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
const invalid = () => Object.assign(new Error('invalid_cover_page'), {code: 'invalid_cover_page'});
const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
const MAX_PAGES = 12, WIDTH = 160;

/** One admitted page preview. The URI is the page's own returned bytes at a
 * bounded width; it is presentation-only and never an upload receipt. */
export type NativeCoverPage = Readonly<{index: number; name: string; uri: string}>;
export type NativeCoverPagesOpening = Readonly<{itemId: string; sourceId: string; revision: string; pageCount: number; pageNames: readonly string[]}>;

function base64(bytes: Uint8Array): string {
  const chunks: string[] = [];
  let chunk = '';
  for (let index = 0; index < bytes.length; index += 3) {
    const a = bytes[index]!, b = bytes[index + 1], c = bytes[index + 2];
    chunk += alphabet[a >> 2]!;
    chunk += alphabet[((a & 3) << 4) | ((b ?? 0) >> 4)]!;
    chunk += b === undefined ? '=' : alphabet[((b & 15) << 2) | ((c ?? 0) >> 6)]!;
    chunk += c === undefined ? '=' : alphabet[c & 63]!;
    if (chunk.length >= 8192) {chunks.push(chunk); chunk = '';}
  }
  if (chunk) chunks.push(chunk);
  return chunks.join('');
}

/** Bounded real page previews from one admitted session for the exact opening.
 * Only the first twelve pages are fetched at thumbnail width; the session is
 * closed before the bytes are returned, and no page name or path is exposed. */
export async function loadNativeCoverPages(options: Readonly<{client: ReadingClient; opening: NativeCoverPagesOpening;
  signal: AbortSignal; isCurrent: () => boolean}>): Promise<readonly NativeCoverPage[]> {
  const opening = options.opening;
  const current = () => !options.signal.aborted && options.isCurrent();
  if (!current()) throw cancelled();
  if (!opening.itemId || !opening.sourceId || !opening.revision
    || opening.pageCount < 1 || opening.pageNames.length !== opening.pageCount) throw invalid();
  const session = await options.client.open({itemId: opening.itemId, sourceId: opening.sourceId}, options.signal);
  let closeError: unknown;
  try {
    if (!current()) throw cancelled();
    const manifest = session.manifest, pageNames = manifest.pageNames;
    if (manifest.itemId !== opening.itemId || manifest.sourceId !== opening.sourceId || manifest.format !== 'cbz'
      || manifest.revision !== opening.revision || !pageNames || pageNames.length !== opening.pageCount) throw invalid();
    const pages: NativeCoverPage[] = [];
    for (let index = 0; index < Math.min(opening.pageCount, MAX_PAGES); index++) {
      if (!current()) throw cancelled();
      const page = await session.page(index, {width: WIDTH, signal: options.signal});
      if (!current()) throw cancelled();
      if (page.index !== index || page.name !== pageNames[index]) throw invalid();
      // The same image formats the cover upload accepts; another format stays
      // selectable through the page number but is never shown as a preview.
      if (page.mime !== 'image/jpeg' && page.mime !== 'image/png') continue;
      pages.push(Object.freeze({index, name: page.name, uri: `data:${page.mime};base64,${base64(page.bytes)}`}));
    }
    return Object.freeze(pages);
  } finally {
    try { await session.close(); } catch (error) { closeError = error; }
    if (closeError && !options.signal.aborted) throw closeError;
  }
}
