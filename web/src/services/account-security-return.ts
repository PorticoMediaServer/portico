export type HostedSecurityArrival = Readonly<{kind: 'transaction'; transactionId: string} | {kind: 'invalid'}>;
/** A URL is only a hint. Scrub its reference before initialization/storage or
 * any cookie coordinator; protected original state chooses the consumer. */
export function captureHostedSecurityArrival(page: Pick<Location, 'href'>, history: Pick<History, 'replaceState'>): HostedSecurityArrival | undefined {
  const url = new URL(page.href);
  if (url.pathname !== '/account' || !url.searchParams.has('identityTransaction')) return;
  const transaction = url.searchParams.get('identityTransaction');
  const keys = [...url.searchParams.keys()];
  const valid = !url.hash && !url.username && !url.password && !keys.some(k => !['identityTransaction', 'tab'].includes(k)) && keys.filter(k => k === 'tab').length <= 1;
  const callback = valid && keys.filter(k => k === 'identityTransaction').length === 1 && /^oid_[A-Za-z0-9_-]{43}$/.test(transaction ?? '') && (!url.searchParams.has('tab') || url.searchParams.get('tab') === 'security');
  history.replaceState(null, '', '/account?tab=security');
  return callback ? Object.freeze({kind: 'transaction', transactionId: transaction!}) : Object.freeze({kind: 'invalid'});
}
