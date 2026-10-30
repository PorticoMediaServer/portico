import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeLayoutView, HomeLayoutRowView} from '@core/home.ts';
import type {PersonalResource} from '@core/personal-saved.ts';
import {addHomeDraftView, homeDraftIntent, homeLayoutDraft, moveHomeDraftRow, removeHomeDraftView, showHomeDraftRow} from '@core/home-layout-editor.ts';
const row = (id: string, patch: Partial<HomeLayoutRowView> = {}): HomeLayoutRowView => ({id, title: id, kind: 'titles', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false, ...patch});
const view: HomeLayoutView = {revision: 7, rowOrder: ['continue', 'fixed', 'recommended', 'view:existing'], hiddenRowIds: ['recommended', 'unreadable-row'],
  rows: [row('continue', {required: true, hideable: false}), row('fixed', {reorderable: false}), row('recommended', {hidden: true}), row('view:existing')]};
const resource: PersonalResource = {serverId: 'actual-server', viewerFence: 'actual-viewer', id: 'added', kind: 'view', name: 'Saved filter', summary: '', visibility: 'private', revision: 3, role: 'owner', entryCount: 4, actions: [],
  definition: {libraryId: 'library', pivot: 'movies', query: null, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'}, status: 'ready', invalidComponents: [], pinned: false, pinRevision: 0, shares: []};
test('required rows remain visible and movable while fixed slots and exact revision survive edits', () => {
  const original = homeLayoutDraft(view), hidden = showHomeDraftRow(original, 'continue', false);
  assert.equal(hidden, original);
  assert.equal(moveHomeDraftRow(original, 'fixed', 0), original);
  const moved = moveHomeDraftRow(original, 'recommended', 0);
  assert.deepEqual(moved.order, ['recommended', 'fixed', 'continue', 'view:existing']);
  const intent = homeDraftIntent(moved);
  assert.deepEqual(intent, {expectedRevision: 7, rowOrder: ['recommended', 'continue', 'view:existing'], hiddenRowIds: ['recommended', 'unreadable-row']});
  assert.deepEqual(original.order, ['continue', 'fixed', 'recommended', 'view:existing']);
});
test('a real ready saved view appends once; removing a view removes its hidden membership', () => {
  let draft = addHomeDraftView(homeLayoutDraft(view), resource);
  assert.equal(addHomeDraftView(draft, resource), draft);
  assert.equal(draft.order.at(-1), 'view:added');assert.equal(draft.added.get('view:added'), resource);
  draft = showHomeDraftRow(draft, 'view:added', false);
  draft = removeHomeDraftView(draft, 'view:added');
  assert.equal(draft.hidden.has('view:added'), false);assert.equal(draft.added.has('view:added'), false);assert.equal(homeDraftIntent(draft).rowOrder.includes('view:added'), false);
  assert.equal(removeHomeDraftView(draft, 'continue'), draft);
});
test('a needs-review view cannot be executed by adding it to Home', () => {
  const draft = homeLayoutDraft(view);
  assert.equal(addHomeDraftView(draft, {...resource, status: 'needs-review', definition: undefined}), draft);
});
