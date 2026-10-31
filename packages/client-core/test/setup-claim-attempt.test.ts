import assert from 'node:assert/strict';
import test from 'node:test';
import {setupApprovalCode} from '../src/setup-claim-attempt.ts';

test('an approval URL must be the hosted claim page for this operation and server', () => {
  const code = 'claim_' + 'A'.repeat(43), good = `https://account.example/claim#code=${code}&operation=op&server=Home`;
  const expect = {hostedOrigin: 'https://account.example', operationId: 'op', serverName: 'Home'};
  assert.equal(setupApprovalCode(good, expect), code);
  for (const url of [good.replace('/claim', '/other'), good.replace('#', '?x=1#'), good.replace('operation=op', 'operation=other'),
    good.replace('server=Home', 'server=Away'), good + '&extra=1', good.replace('code=claim_', 'code=other_'), 'https://evil.example/claim#' + good.split('#')[1]])
    assert.equal(setupApprovalCode(url, expect), undefined, url);
  assert.throws(() => setupApprovalCode(good.replace(code, 'claim_short'), expect));
});
