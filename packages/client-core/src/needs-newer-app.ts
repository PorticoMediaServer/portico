/** A whole response is a tagged union and this app does not know the tag the server sent (R-09b):
 * the server is newer than this app. It is an honest "update the app" state, never a guess at the shape. */
export class NeedsNewerAppError extends Error {
  readonly code = 'needs_newer_app';
  readonly tag: string;
  constructor(tag: string) {
    super('This server needs a newer version of the app.');
    this.name = 'NeedsNewerAppError';
    this.tag = tag;
  }
}
/** The required const tag of a response object: absent or not a string is an unreadable response (`invalid`),
 * a tag outside `known` is a newer server. */
export function requireTag(raw: unknown, key: string, known: readonly string[], invalid: () => never): string {
  const tag = typeof raw === 'object' && raw !== null && !Array.isArray(raw) ? (raw as Record<string, unknown>)[key] : undefined;
  if (typeof tag !== 'string' || !tag) invalid();
  if (!known.includes(tag)) throw new NeedsNewerAppError(tag);
  return tag;
}
