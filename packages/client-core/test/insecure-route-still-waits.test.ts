import test from 'node:test';
import assert from 'node:assert/strict';
import {presentError} from '../src/presentation/errors.ts';
import {RouteError} from '../src/route-identity.ts';
import {INSECURE_ROUTE_UNCONFIRMED} from '../src/route-connection.ts';

// Before this change a phone that left home, whose server has only a plain-HTTP address, got
// route_unavailable from the route check: "unreachable", which every screen treats as "wait
// and keep trying". The new reason must still be that, or launch shows a page failure and
// nothing reconnects when the phone is home again.
test('a plain route this device could not confirm is still "the server cannot be reached"', () => {
  const before = presentError(new RouteError('route_unavailable', 'x'), 'generic', {deviceOnline: true}).category;
  const now = presentError(new RouteError(INSECURE_ROUTE_UNCONFIRMED, 'x'), 'generic', {deviceOnline: true}).category;
  assert.equal(before, 'unreachable');
  assert.equal(now, 'unreachable');
});
