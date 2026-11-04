/** RD-8 (DECISIONS W-19): only the server-wide reading-session cap answers busy, and the reading client
 * has already waited out its Retry-After once. The reader's failed page then says the server is busy,
 * in the catalogue's words, and keeps Try again; no separate toast repeats it. */
export const readingServerBusy = (error: unknown): boolean =>
  !!error && typeof error === 'object' && (error as {code?: unknown}).code === 'reading_sessions_busy';
