export const localDevicePath = '/device';
/** A URL code is a display hint only. It never starts review or approval. */
export function createLocalDeviceHint() {
  let hint: Readonly<{origin: string; code: string}> | undefined;
  return Object.freeze({
    capture(location: Readonly<{href: string}>, history: Pick<History, 'replaceState' | 'state'>): boolean {
      const url = new URL(location.href);
      if (url.pathname !== localDevicePath || !url.hash) return false;
      hint = undefined;
      const values = new URLSearchParams(url.hash.slice(1));
      // Scrub before publishing even malformed input. Keep the real history entry.
      history.replaceState(history.state, '', url.origin + url.pathname + url.search);
      const code = values.get('code');
      if ([...values.keys()].length === 1 && values.getAll('code').length === 1 && code
        && code.length <= 10 && [...code.toUpperCase()].every(value => 'ABCDEFGHJKMNPQRSTUVWXYZ23456789-'.includes(value))) {
        const canonical = code.toUpperCase().replaceAll('-', '');
        if (canonical.length === 8) hint = Object.freeze({origin: url.origin, code: canonical.slice(0, 4) + '-' + canonical.slice(4)});
      }
      return true;
    },
    take(origin: string): string | undefined {
      const original = hint;hint = undefined;return original?.origin === origin ? original.code : undefined;
    },
    clear() {hint = undefined;},
  });
}
// No import-time browser/storage access. The router captures before parsing URLs.
export const localDeviceHint = createLocalDeviceHint();
