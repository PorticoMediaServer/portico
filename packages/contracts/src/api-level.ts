/** The newest API major the server in this repository speaks (`apiLevel` in `GET /v1/system`).
 * The server's `APILevel` (server/internal/httpapi/system_info.go) carries the same number. */
export const API_LEVEL = 1;

/** The additive minor of that major; for support and diagnostics only (O-159). Matches `APIMinor`. */
export const API_MINOR = 0;

/** Every capability a server may advertise: features added after 1.0, one stable ID each,
 * never renamed or reused. Empty at 1.0. Matches the server's `Capabilities` registry. */
export const CAPABILITIES: readonly string[] = [];
