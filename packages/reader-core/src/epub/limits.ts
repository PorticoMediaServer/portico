import {ReaderError} from '../errors.ts';

/** RD-5: the EPUB size bounds. An online session's manifest carries the server's own `limits`
 * (`memberBytes`, `totalBytes`), and every reader check uses them, so the reader and the server
 * can't disagree; a local or offline file without a manifest uses these defaults (the server's). */
export type EpubLimits = Readonly<{memberBytes: number; totalBytes: number}>;
export const EPUB_LIMITS: EpubLimits = Object.freeze({memberBytes: 16 * 1024 * 1024, totalBytes: 256 * 1024 * 1024});

/** The source's own limits when it has them, else the defaults; malformed limits refuse the book. */
export function epubLimits(limits: EpubLimits | undefined): EpubLimits {
  if (limits === undefined) return EPUB_LIMITS;
  const {memberBytes, totalBytes} = limits;
  if (!Number.isSafeInteger(memberBytes) || !Number.isSafeInteger(totalBytes) || memberBytes < 1 || totalBytes < memberBytes) throw new ReaderError('invalid_source');
  return Object.freeze({memberBytes, totalBytes});
}
