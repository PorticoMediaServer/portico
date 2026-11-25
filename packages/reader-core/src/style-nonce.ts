/** Reads the app document's CSP style nonce, ignoring empty strings and the unreplaced build placeholder. */
export function styleNonce(doc: Pick<Document, 'querySelector'> | undefined = globalThis.document): string | undefined {
  const nonce = doc?.querySelector?.<HTMLMetaElement>('meta[name="portico-style-nonce"]')?.content?.trim();
  return nonce && nonce !== '__PORTICO_STYLE_NONCE__' ? nonce : undefined;
}

/** Creates a `<style>` element in `target` carrying the CSP nonce from `app` (defaulting to `target`). */
export function createNonceStyle(target: Document, css: string, app: Pick<Document, 'querySelector'> = target): HTMLStyleElement {
  const style = target.createElement('style');
  const nonce = styleNonce(app);
  if (nonce) style.nonce = nonce;
  style.textContent = css;
  return style;
}
