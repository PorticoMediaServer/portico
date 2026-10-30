export type LinkFragment = Readonly<{token?: string}>;
/** Scrub BEFORE parsing, storage, rendering or asynchronous work, including malformed URLs. */
export function takeLinkFragment(location: Pick<Location, 'hash' | 'pathname' | 'search'>,
  history: Pick<History, 'state' | 'replaceState'>): LinkFragment {
  const fragment = location.hash;
  if (fragment) history.replaceState(history.state, '', location.pathname + location.search);
  const params = new URLSearchParams(fragment.slice(1));
  if (location.search || params.size !== 1 || params.getAll('token').length !== 1) return {};
  const token = params.get('token') ?? '';
  return token.length > 0 && token.length <= 4096 && /^[A-Za-z0-9_-]+$/.test(token) ? {token} : {};
}

export type VerificationLink = Readonly<{kind: 'registration' | 'email_change'; resource: string; code: string}>;
/** Hosted mailoutbox/links.go's versioned tuple. Provider-contact mail is retired. */
export function verificationLink(token: string): VerificationLink | undefined {
  if (token.length > 2048 || !/^[A-Za-z0-9_-]+$/.test(token)) return;
  try {
    const base64 = token.replace(/-/g, '+').replace(/_/g, '/');
    const value: unknown = JSON.parse(atob(base64 + '='.repeat((4 - base64.length % 4) % 4)));
    if (!Array.isArray(value) || value.length !== 4 || value[0] !== '1' ||
      !['registration', 'email_change'].includes(value[1]) ||
      typeof value[2] !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(value[2]) ||
      typeof value[3] !== 'string' || !/^\d{6}$/.test(value[3])) return;
    return {kind: value[1], resource: value[2], code: value[3]};
  } catch { return; }
}
