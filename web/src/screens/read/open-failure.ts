/** RD-8 (DECISIONS W-19): only the server-wide reading-session cap answers busy, and the reading client
 * has already waited out its Retry-After once. The reader's failed page then says the server is busy,
 * in the catalogue's words, and keeps Try again; no separate toast repeats it. */
export const readingServerBusy = (error: unknown): boolean =>
  !!error && typeof error === 'object' && (error as {code?: unknown}).code === 'reading_sessions_busy';

/** RD-5: the server refuses to open an EPUB too large to read (422 `reading_publication_too_large`,
 * retry never). The reader says so plainly in its open-failure state, with no Try again and no toast. */
export const publicationTooLarge = (error: unknown): boolean =>
  !!error && typeof error === 'object' && (error as {code?: unknown}).code === 'reading_publication_too_large';
