import {CodedError} from '@core/server-messages.ts';

export type PorticoReturnPending = Readonly<{kind: 'owner' | 'setup'; transactionId: string}>;
export type CapturedPorticoReturn = Readonly<{href: string; fragment: string}>;
/** Run in the callback's first synchronous render, before storage or network.
 * The captured value stays in memory; refresh cannot replay the URL fragment. */
export function capturePorticoReturn(location: Pick<Location, 'href'>, history: Pick<History, 'replaceState'>): CapturedPorticoReturn {
  const url = new URL(location.href), fragment = url.hash;
  url.hash = '';
  history.replaceState(null, '', url.pathname + url.search);
  return Object.freeze({href: new URL(location.href).origin + url.pathname + url.search, fragment});
}
/** KIND comes only from the protected, captured pending record. Neither URL
 * purpose nor a missing code selects owner authority. This selects a consumer;
 * it does not verify any owner/start/registration or issue a credential. */
export function dispatchPorticoReturn(captured: CapturedPorticoReturn, pending: PorticoReturnPending): Readonly<{kind: PorticoReturnPending['kind']; transactionId: string; href: string}> {
  const invalid = () => new CodedError('invalid_return', 'This return no longer matches the pending sign-in.');
  const url = new URL(captured.href), values = new URLSearchParams(captured.fragment.slice(1));
  const keys = [...values.keys()].sort().join(',');
  if (url.pathname !== '/auth/portico-return' || url.search || url.hash || url.username || url.password ||
      captured.fragment.length > 2048 || !captured.fragment.startsWith('#')) throw invalid();
  if (pending.kind === 'owner' || pending.kind === 'setup') {
    // The protected continuation producer's opaque id is captured in protected storage. Do not
    // invent a prefix or reinterpret an ordinary return id as an owner id.
    if (keys !== 'transaction' || values.getAll('transaction').length !== 1 || !/^[A-Za-z0-9_-]{1,128}$/.test(pending.transactionId) || values.get('transaction') !== pending.transactionId) throw invalid();
  } else throw invalid();
  return Object.freeze({kind: pending.kind, transactionId: values.get('transaction')!, href: captured.href + captured.fragment});
}
