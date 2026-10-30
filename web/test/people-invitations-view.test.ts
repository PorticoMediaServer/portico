import test from 'node:test';
import assert from 'node:assert/strict';
import type {ServerInvitation} from '@core/direct-account.ts';
import {InvitationsView, type InvitationScope} from '../src/screens/settings/people-invitations-view.ts';
const query = {search: '', filters: {status: 'all'}, sort: {by: 'sent', direction: 'desc' as const}};
const row: ServerInvitation = {id: 'invitation', revision: 7, role: 'member', state: 'pending', createdAt: '2026-10-23T12:00:00Z', expiresAt: '2000-01-01T00:00:00Z', allowedLibraries: []};
function load(view: InvitationsView, scope: InvitationScope, rows = [row]) {
  const read = view.beginRead(scope)!;
  return view.acceptRead(read, {items: rows, nextCursor: 'next', matchedCount: 73, totalCount: 101})!;
}
function fixture() {
  let current = true, ids = 0;
  const view = new InvitationsView(() => current), scope = view.replace(query), page = load(view, scope);
  return {view, scope, page, row: page.value.items[0], ids: () => 'op-' + ++ids, retireAuthority: () => {current = false;}};
}
test('query capture is immutable and old page/copy/read callbacks cannot acquire replacement', () => {
  const f = fixture(), heldCopy = () => f.view.rowCurrent(f.page, f.row);
  const more = f.view.beginRead(f.scope, f.page)!;
  assert.equal(f.view.beginRead(f.scope, f.page), undefined, 'synchronous singleflight before React rerenders');
  f.view.retireScope(f.scope);
  const next = f.view.replace({...query, search: 'other'}), page = load(f.view, next, [{...row, id: 'other'}]);
  assert.equal(heldCopy(), false);
  assert.equal(more.life.signal.aborted, true);
  assert.equal(f.view.acceptRead(more, f.page.value), undefined);
  assert.equal(f.view.refuseRead(more), false, 'old finally cannot settle successor');
  assert.equal(f.view.pageCurrent(page), true);
  assert.equal(f.view.beginRead(f.scope, f.page), undefined);
  assert.ok(Object.isFrozen(next.query.filters));assert.ok(Object.isFrozen(next.query.sort));
});
test('even append replacement retires original exact row and late Copy link publication', () => {
  const f = fixture(), copied = () => f.view.rowCurrent(f.page, f.row);
  const read = f.view.beginRead(f.scope, f.page)!;
  const next = f.view.acceptRead(read, {...f.page.value, items: [{...row, id: 'second'}]})!;
  assert.equal(copied(), false);assert.equal(f.page.life.signal.aborted, true);
  assert.equal(f.view.rowCurrent(next, f.row), true);
  assert.equal(f.view.rowCurrent(next, {...row}), false, 'same id/revision is not resident object identity');
  assert.equal(next.value.matchedCount, 73);assert.equal(next.value.totalCount, 101);
});
test('confirmation requires committed opening and is synchronously claimed once', () => {
  const f = fixture(), opening = f.view.open(f.page, f.row, null)!;
  assert.equal(f.view.beginRevoke(opening, f.ids), undefined);
  const cleanup = f.view.commitOpening(opening), attempt = f.view.beginRevoke(opening, f.ids)!;
  assert.equal(f.view.beginRevoke(opening, f.ids), undefined);
  assert.equal(f.view.cancel(opening), false);
  assert.equal(attempt.operationId, 'op-1');assert.equal(attempt.opening.row, f.row);
  cleanup();assert.equal(f.view.revokeCurrent(attempt), false, 'uncommitted modal cannot publish');
});
test('Cancel A then reopen B defeats retained Confirm/Cancel A without clearing B', () => {
  const f = fixture(), a = f.view.open(f.page, f.row, null)!;
  f.view.commitOpening(a);
  const heldConfirm = () => f.view.beginRevoke(a, f.ids), heldCancel = () => f.view.cancel(a);
  assert.equal(f.view.cancel(a), true);
  const b = f.view.open(f.page, f.row, null)!;f.view.commitOpening(b);
  assert.equal(heldConfirm(), undefined);assert.equal(heldCancel(), false);
  assert.equal(f.view.openingCurrent(b), true);
  assert.equal(f.view.beginRevoke(b, f.ids)?.operationId, 'op-1', 'retired callbacks allocate no request id');
});
test('uncertain retry repeats exact row/CAS/id, and prior dispatch cannot settle the retry', () => {
  const f = fixture(), opening = f.view.open(f.page, f.row, null)!;f.view.commitOpening(opening);
  const first = f.view.beginRevoke(opening, f.ids)!;
  assert.equal(f.view.settleRevoke(first, false), true);
  const retry = f.view.beginRevoke(opening, f.ids)!;
  assert.equal(retry.operationId, first.operationId);assert.equal(retry.opening.row, first.opening.row);
  assert.notEqual(retry, first);
  assert.equal(f.view.settleRevoke(first, true), false);
  assert.equal(f.view.revokeCurrent(retry), true);
  assert.equal(f.view.settleRevoke(retry, true), true);
  assert.equal(f.view.beginRevoke(opening, f.ids), undefined, 'accepted opening is consumed');
});
test('late request A cannot clear B after page replacement, even with same row id', () => {
  const f = fixture(), a = f.view.open(f.page, f.row, null)!;f.view.commitOpening(a);
  const first = f.view.beginRevoke(a, f.ids)!;
  const nextScope = f.view.replace(query), nextPage = load(f.view, nextScope, [{...row, revision: 8}]);
  const b = f.view.open(nextPage, nextPage.value.items[0], null)!;f.view.commitOpening(b);
  const next = f.view.beginRevoke(b, f.ids)!;
  assert.equal(a.life.signal.aborted, true);
  assert.equal(f.view.settleRevoke(first, true), false);assert.equal(f.view.settleRevoke(first, false), false);
  assert.equal(f.view.revokeCurrent(next), true);assert.equal(next.opening.row.revision, 8);
});
test('retired component/authority and missing entropy cannot resurrect callbacks', () => {
  for (const end of ['view', 'authority']) {
    const f = fixture(), opening = f.view.open(f.page, f.row, null)!;f.view.commitOpening(opening);
    assert.throws(() => f.view.beginRevoke(opening, () => {throw new Error('entropy');}), /entropy/);
    assert.equal(opening.running, undefined);
    if (end === 'view') f.view.retire();else f.retireAuthority();
    assert.equal(f.view.beginRevoke(opening, f.ids), undefined);assert.equal(f.view.rowCurrent(f.page, f.row), false);
  }
});
test('layout replay creates a new view without reviving retired request/opening tokens', () => {
  const first = fixture(), opening = first.view.open(first.page, first.row, null)!;
  first.view.commitOpening(opening);first.view.retire();
  const renewed = fixture();
  assert.equal(renewed.view.pageCurrent(renewed.page), true);
  assert.equal(first.view.beginRead(first.scope), undefined);
  assert.equal(first.view.beginRevoke(opening, first.ids), undefined);
  assert.equal(first.view.rowCurrent(first.page, first.row), false);
});
