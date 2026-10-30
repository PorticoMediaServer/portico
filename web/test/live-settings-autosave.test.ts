import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {DraftForm} from '../../packages/client-core/src/server-admin/draft-form.ts';
import {AutoSaveForm} from '../src/screens/live/owner/auto-save-form.ts';

const settled = () => new Promise<void>(resolve => setImmediate(resolve));

test('live settings coalesce edits and save one revisioned draft', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const writes: {number: number; enabled: boolean}[] = [];
  const raw = new DraftForm({load: async () => ({value: {number: 1, enabled: true}}), save: async value => {writes.push(value);return {value};}});
  const form = new AutoSaveForm(raw, () => true, error => {throw error;});
  await form.load();
  form.set({number: 2});context.mock.timers.tick(300);form.set({enabled: false});context.mock.timers.tick(499);
  assert.deepEqual(writes, []);
  context.mock.timers.tick(1);await settled();
  assert.deepEqual(writes, [{number: 2, enabled: false}]);
  assert.equal(form.getSnapshot().dirty, false);
  form.dispose();
});

test('the form stays locked until the saved revision has been reread', async () => {
  let reads = 0, finishRead: ((value: {value: {number: number}; status: {revision: number}}) => void) | undefined;
  const raw = new DraftForm<{number: number}, {revision: number}>({
    load: async () => ++reads === 1 ? {value: {number: 1}, status: {revision: 1}} : new Promise(resolve => {finishRead = resolve;}),
    save: async () => {},
  });
  const form = new AutoSaveForm(raw, () => true, error => {throw error;});
  await form.load();form.set({number: 2});
  const saving = form.save();await settled();
  assert.equal(raw.getSnapshot().saving, false);
  assert.equal(form.getSnapshot().saving, true);
  form.set({number: 3});assert.deepEqual(form.getSnapshot().draft, {number: 2});
  await form.load();assert.equal(reads, 2);
  assert.ok(finishRead);finishRead({value: {number: 2}, status: {revision: 2}});await saving;
  assert.equal(form.getSnapshot().saving, false);
  assert.equal(form.getSnapshot().status?.revision, 2);
  form.dispose();
});

test('a failed autosave retains the draft and waits for explicit retry', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const problem = new Error('connection_lost'), failures: unknown[] = [];let calls = 0;
  const raw = new DraftForm({load: async () => ({value: {number: 1}}), save: async value => {if (++calls === 1) throw problem;return {value};}});
  const form = new AutoSaveForm(raw, () => true, error => failures.push(error));
  await form.load();form.set({number: 2});context.mock.timers.tick(500);await settled();
  assert.deepEqual(failures, [problem]);assert.equal(form.getSnapshot().saveError, problem);
  assert.deepEqual(form.getSnapshot().draft, {number: 2});assert.equal(form.getSnapshot().dirty, true);
  context.mock.timers.tick(5000);await settled();assert.equal(calls, 1);
  await form.save();assert.equal(calls, 2);assert.equal(form.getSnapshot().dirty, false);
  form.dispose();
});

test('discard, retirement and disposal prevent delayed settings writes', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});let calls = 0, current = true;
  const raw = new DraftForm({load: async () => ({value: {number: 1}}), save: async value => {calls++;return {value};}});
  const form = new AutoSaveForm(raw, () => current, error => {throw error;});
  await form.load();form.set({number: 2});form.discard();context.mock.timers.tick(500);await settled();assert.equal(calls, 0);
  form.set({number: 3});current = false;context.mock.timers.tick(500);await settled();assert.equal(calls, 0);
  current = true;form.set({number: 4});form.dispose();context.mock.timers.tick(500);await settled();assert.equal(calls, 0);
});

test('invalid drafts never autosave', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});let calls = 0;
  const raw = new DraftForm({load: async () => ({value: {name: 'Current'}}), validate: value => value.name ? undefined : 'required', save: async value => {calls++;return {value};}});
  const form = new AutoSaveForm(raw, () => true, error => {throw error;});
  await form.load();form.set({name: ''});context.mock.timers.tick(500);await settled();assert.equal(calls, 0);
  form.set({name: 'New'});context.mock.timers.tick(500);await settled();assert.equal(calls, 1);
  form.dispose();
});

test('autosave quiet refresh defers for pending debounce and failed-save acknowledgement',async context=>{
 context.mock.timers.enable({apis:['setTimeout']});let reads=0,fail=false;const error=new Error('original');const raw=new DraftForm({load:async()=>{reads++;return {value:{number:1}};},save:async value=>{if(fail)throw error;return {value};}}),form=new AutoSaveForm(raw,()=>true,()=>{});
 await form.load();form.set({number:2});assert.deepEqual(await form.refresh(),{kind:'deferred'});assert.equal(reads,1);fail=true;await form.save();const before=form.getSnapshot();assert.deepEqual(await form.refresh(),{kind:'deferred'});assert.equal(form.getSnapshot(),before);assert.equal(form.getSnapshot().saveError,error);form.discard();assert.deepEqual(await form.refresh(),{kind:'adopted'});assert.equal(reads,2);form.dispose();
});
