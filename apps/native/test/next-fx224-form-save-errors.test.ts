import test from 'node:test';
import assert from 'node:assert/strict';
import {DraftForm, FormSet} from '../../../packages/client-core/src/server-admin/draft-form';

// FX-224 source: real draft-form/FormSet behavior behind the General and Remote
// save-error projection. These cases exercise actual retained drafts, saveError
// and the FormSet's first-failure stop; no presenter strings or fake forms.
type Doc = {value: string};
const loaded = (value = 'saved') => ({value: {value}});

function form(options: Partial<ConstructorParameters<typeof DraftForm<Doc>>[0]> = {}) {
  return new DraftForm<Doc>({
    load: async () => loaded(),
    save: async () => loaded('stored'),
    ...options,
  });
}

test('a rejected save projects saveError while the dirty draft and saved value stay', async () => {
  const failure = new Error('transport failed');
  let attempts = 0, savedValue = 'saved';
  const actual = form({save: async draft => {
    attempts++;
    if (attempts === 1) throw failure;
    savedValue = draft.value;
    return {value: {value: draft.value}};
  }});
  await actual.load();
  actual.set({value: 'unsaved edit'});
  await assert.rejects(actual.save(), failure);
  let state = actual.getSnapshot();
  assert.strictEqual(state.saveError, failure);
  assert.equal(state.draft?.value, 'unsaved edit');
  assert.equal(state.saved?.value, 'saved');
  assert.equal(state.dirty, true);
  assert.equal(attempts, 1);
  await actual.save();
  state = actual.getSnapshot();
  assert.equal(state.saveError, undefined);
  assert.equal(state.draft?.value, 'unsaved edit');
  assert.equal(state.saved?.value, 'unsaved edit');
  assert.equal(savedValue, 'unsaved edit');
  assert.equal(state.dirty, false);
  actual.dispose();
});

test('a conflict failure retains the owner draft and saveError for deliberate retry', async () => {
  const conflict = Object.assign(new Error('conflict'), {code: 'conflict'});
  let revision = 1;
  const actual = form({
    load: async () => ({value: {value: revision === 1 ? 'saved' : 'newer'}}),
    save: async () => {throw conflict;},
  });
  await actual.load();
  actual.set({value: 'owner edit'});
  await assert.rejects(actual.save(), conflict);
  const state = actual.getSnapshot();
  assert.strictEqual(state.saveError, conflict);
  assert.equal(state.draft?.value, 'owner edit');
  assert.equal(state.saved?.value, 'saved');
  assert.equal(state.dirty, true);
  actual.dispose();
});

test('a local validation problem stays separate from saveError and blocks Save', async () => {
  const actual = form({validate: draft => draft.value.trim() ? undefined : 'Name is required'});
  await actual.load();
  actual.set({value: '   '});
  await assert.rejects(actual.save(), /Name is required/);
  const state = actual.getSnapshot();
  assert.equal(state.problem, 'Name is required');
  assert.equal(state.saveError, undefined);
  assert.equal(state.dirty, true);
  actual.dispose();
});

test('FormSet stops at a partial multi-form failure and keeps the unsaved form dirty', async () => {
  const failure = new Error('second rejected');
  const first = form(), second = form({save: async () => {throw failure;}});
  const set = new FormSet();
  set.add('first', first); set.add('second', second);
  await first.load(); await second.load();
  first.set({value: 'first edit'}); second.set({value: 'second edit'});
  assert.equal(await set.save(), false);
  const state = set.getSnapshot();
  assert.strictEqual(state.saveError, failure);
  assert.equal(state.dirty, true);
  assert.equal(first.getSnapshot().dirty, false, 'the first accepted form is stored');
  assert.equal(second.getSnapshot().dirty, true, 'the failed form keeps its unsaved draft');
  assert.equal(second.getSnapshot().draft?.value, 'second edit');
  first.dispose(); second.dispose();
});
