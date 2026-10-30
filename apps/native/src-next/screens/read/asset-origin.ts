export const appleReaderEntry = 'portico-reader://bundle/index.html';
export const androidReaderEntry = 'https://appassets.androidplatform.net/PorticoReader.bundle/index.html';
export function readerAssetEntry(url: unknown): url is string {
  return url === appleReaderEntry || url === androidReaderEntry;
}
/** originWhitelist must pass every scheme to this gate: WebView16 otherwise
 * sends rejected origins to Linking before consulting onShouldStartLoad. */
export function readerNavigationAllowed(entry: string, url: string, isTopFrame: boolean): boolean {
  if (!readerAssetEntry(entry) || typeof url !== 'string' || url.length > 8192) return false;
  if (url === entry || url.startsWith(entry + '#')) return true;
  // These are the checked renderer's sandbox documents, never a top-level page.
  return isTopFrame === false && (url === 'about:blank' || url.startsWith('blob:'));
}
export function readerDelivery(message: string): string {
  if (message.length > 2 * 1024 * 1024) throw new Error('invalid_bridge');
  return `window.PorticoReader&&window.PorticoReader.receive(${JSON.stringify(message).replace(/</g, '\\u003c').replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029')});true;`;
}
