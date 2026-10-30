/** A distinct human-entry capability, never a shortened long-link secret. Only
 * ASCII case and four hyphen-separated groups are equivalent; no URL is read. */
export function parseShortInvitationCode(raw: unknown): string | undefined {
  if (typeof raw !== 'string') return;
  const code = raw.length === 16 && /^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{16}$/.test(raw) ? raw
    : raw.length === 19 && /^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{4}(?:-[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{4}){3}$/.test(raw) ? raw.replaceAll('-', '') : undefined;
  return code?.toUpperCase();
}

export function formatShortInvitationCode(raw: unknown): string | undefined {
  const code = parseShortInvitationCode(raw);
  return code === undefined ? undefined : `${code.slice(0, 4)}-${code.slice(4, 8)}-${code.slice(8, 12)}-${code.slice(12)}`;
}
