/** Mobile web Settings is a full page with Done (owner-libraries 24, owner-dashboard 9, live-owner 18): Done leaves
 * Settings for the page it was opened from. The router reports each resolved location; the shell reports the
 * authority it runs under (server, account, profile). A remembered page belongs to the authority it was seen under:
 * after a server or profile switch, or a sign-out, Done goes Home rather than to the old authority's page. */
const settingsPaths = new Set(['/settings', '/dashboard']);
type Seen = Readonly<{pathname: string; href: string}>;
let authority: string | undefined, remembered: Readonly<{href: string; authority: string}> | undefined, current: (() => Seen) | undefined;

/** The router names where it is, so the shell can remember the page it was on when it comes up. */
export function trackSettingsReturn(location: () => Seen): void { current = location; }

/** A location resolved. Only a page seen under a running authority is remembered, never a sign-in or loading page. */
export function rememberSettingsReturn(): void {
  const seen = current?.();
  if (authority !== undefined && seen && !settingsPaths.has(seen.pathname)) remembered = {href: seen.href, authority};
}

/** The shell's authority: a key for the running server/account/profile, or undefined while it restores or is signed
 * out (nothing is remembered then). A different key, or a sign-out, forgets the remembered page. */
export function settingsReturnAuthority(key: string | undefined, signedOut = false): void {
  if (signedOut || key !== undefined && remembered && remembered.authority !== key) remembered = undefined;
  const coming = key !== undefined && key !== authority;
  authority = key;
  if (coming && !remembered) rememberSettingsReturn();
}

/** Where Settings' Done goes: the page Settings was opened from under this authority, or Home. */
export function settingsReturnHref(): string {
  return remembered && remembered.authority === authority ? remembered.href : '/';
}
