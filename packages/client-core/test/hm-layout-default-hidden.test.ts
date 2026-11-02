import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeLayoutRowView, HomeLayoutView} from '../src/home.ts';
import {homeDraftIntent, homeLayoutDraft, showHomeDraftRow} from '../src/home-layout-editor.ts';

const row = (id: string, patch: Partial<HomeLayoutRowView> = {}): HomeLayoutRowView => ({
  id, title: id, kind: 'titles', artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: false, ...patch,
});

/**
 * The server's `community_watching` row is DefaultHidden: it is off Home unless
 * the saved order names it (`home_rows.go` homeRowSpec.DefaultHidden and
 * homeRowSpec.hiddenIn), and `GET /v1/home/layout` reports that effective state in
 * rows[].hidden. A new profile has an empty order and an empty hidden set.
 */
const view: HomeLayoutView = {
  revision: 3,
  rowOrder: [],
  hiddenRowIds: [],
  rows: [
    row('continue', {required: true, hideable: false}),
    row('community_watching', {hidden: true}),
    row('new_on_server'),
  ],
};

test('a default-hidden row reads as off in the draft and stays off after Save', () => {
  const draft = homeLayoutDraft(view);
  // The dialog's switch draws `!draft.hidden.has(id)`; a row the server says is
  // hidden must read as off.
  assert.equal(draft.hidden.has('community_watching'), true, 'default-hidden row must start hidden in the draft');
  assert.equal(showHomeDraftRow(draft, 'community_watching', false).hidden.has('community_watching'), true);
  // Saving the untouched draft must not put the row on Home: the intent either
  // keeps it out of the order or names it hidden.
  const intent = homeDraftIntent(draft);
  const onHome = intent.rowOrder.includes('community_watching') && !intent.hiddenRowIds.includes('community_watching');
  assert.equal(onHome, false, 'an untouched Save must not turn a default-hidden row on');
});
