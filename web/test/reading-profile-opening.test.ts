import assert from 'node:assert/strict';
import test from 'node:test';
import {RemoteSourceService, type RemoteSource, type SourceAdvanced, type SourceReadSettings} from '../../packages/client-core/src/remote-sources.ts';
import {sourceChoiceFields, type SourceChoiceDefinition} from '../../packages/client-core/src/remote-source-choice.ts';
import type {LibraryContentApi} from '../../packages/client-core/src/library-content.ts';
import {ReadingProfileOpening} from '../src/screens/settings/remote-sources/reading-profile-opening.ts';

// Authored UNRUN composition vectors: the real SDK owns provenance, receipt
// parsing, mutation exclusion and retained intent. Only HTTP is a held fixture.
const operation = '1788580000000-12345678-1234-1234-1234-123456789012';
const reading = Object.freeze({scanDepth: 'basic', wholeFileReads: false, connections: 4, requestsPerSecond: 4, bytesPerSecond: 0} as const);
const advanced: SourceAdvanced = Object.freeze({scanIntervalSeconds: 21600, watchForChanges: false, scanOnStart: false,
  fullRecheckDays: 0, cacheBytes: 0, cacheFloorBytes: 0, seekPreviews: true, chapterImages: true, waveforms: true,
  loudness: true, checksums: true, introsMode: 'inherit', doubleCheckFolders: false, dirCacheSeconds: 0,
  readBufferBytes: 0, connectTimeoutSeconds: 0, idleTimeoutSeconds: 0, retries: 0, lowLevelRetries: 0});
const definitions: readonly SourceChoiceDefinition[] = Object.freeze((['list', 'once', 'everything', 'ask'] as const)
  .map(name => Object.freeze({name, definitionRevision: 1, reading, advanced})));
const source: RemoteSource = Object.freeze({id: 'remote', kind: 'rclone', name: 'Remote', rootPath: '/remote', generation: 7,
  state: 'ready', rangeSupport: 'conditional', insecureLocal: false, credentialPresent: true});
const wire = {serverId: 'server', viewerFence: 'owner'};
function profile(saved = true): SourceReadSettings {
  return {sourceId: source.id, generation: '7', revision: saved ? 2 : 0, saved, configurationGeneration: '11',
    values: {...reading, wholeFileReads: true, allowAdvancedMediaAnalysis: false}, choiceDefinitions: definitions, choiceApplicableFields: sourceChoiceFields};
}
function parent(readRevision = 2, advancedRevision = 6): SourceReadSettings {
  return {...profile(), revision: readRevision, choiceBaseline: {sourceId: source.id, generation: '7', configurationGeneration: '11', name: 'once',
    definitionRevision: 1, readRevision, advancedRevision, reading, advanced, currentAdvanced: advanced,
    applicableFields: sourceChoiceFields, changedFields: []}};
}
function receipt(settings = parent()) {return {scope: wire, receipt: {operationId: operation, accepted: true, removed: false, source, readSettings: settings}};}
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});
  return {promise, resolve, reject};
}
type Call = {path: string; method: string; body: unknown; signal?: AbortSignal};
function fixture(options: {put?: (call: Call) => Promise<unknown>; advancedPut?: (call: Call) => Promise<unknown>; recover?: () => Promise<unknown>;
  fresh?: () => Promise<unknown>; initial?: SourceReadSettings; unsaved?: boolean; advanced?: () => Promise<unknown>} = {}) {
  const calls: Call[] = [];let submitted = false, live = true;
  const api: LibraryContentApi = {request: async<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    const call = {path, method, body, signal};calls.push(call);
    let value: unknown;
    if (method === 'PUT' && path.endsWith('/settings/advanced')) {
      const response = pairedAdvanced(body && typeof body === 'object' && 'values' in body ? body.values as SourceAdvanced : advanced);
      value = options.advancedPut ? await options.advancedPut(call) : {...response, settings: {...response.settings, revision: 7}};
    }
    else if (method === 'PUT') {submitted = true;value = options.put ? await options.put(call) : receipt();}
    else if (path.includes('/source-operations/')) value = options.recover ? await options.recover() : receipt();
    else if (path.endsWith('/settings/advanced')) value = options.advanced ? await options.advanced() : {scope: wire,
      settings: {sourceId: source.id, generation: '7', revision: options.unsaved ? 0 : 5, saved: !options.unsaved,
        cacheSupported: true, cacheApplied: true, cacheUsedBytes: 0, scanIntervalObserved: 0, values: advanced}};
    else if (path.endsWith('/settings/read-profile')) value = submitted ? (options.fresh ? await options.fresh() : {scope: wire, settings: parent()})
      : {scope: wire, settings: options.initial ?? profile(!options.unsaved)};
    else throw new Error('Unexpected fixture request: ' + method + ' ' + path);
    return value as T;
  }};
  const service = new RemoteSourceService(api, {serverId: 'server', viewerId: 'owner'}, async () => operation);
  const opening = new ReadingProfileOpening(service, source, () => live, () => !service.getSnapshot().busy && !service.getSnapshot().ambiguous);
  const draft = () => {const state = opening.getSnapshot();assert.equal(state.kind, 'ready');if (state.kind !== 'ready') throw new Error('No draft');return state.draft;};
  return {opening, service, calls, draft, retireAuthority: () => {live = false;}};
}

test('original service-issued pair and exact offered object reach one atomic PUT, including first apply without baseline', async () => {
  const held = deferred<unknown>(), f = fixture({unsaved: true, put: () => held.promise});await f.opening.load();
  const original = f.draft(), choice = original.profile.choiceDefinitions![1]!;
  f.opening.select(original, {...choice});assert.equal(f.draft(), original, 'a cloned offer is refused');
  f.opening.select(original, choice);const selected = f.draft();
  const save = f.opening.save(selected, f.opening.getSnapshot());void f.opening.save(selected, f.opening.getSnapshot());
  await Promise.resolve();await Promise.resolve();
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
  const body = f.calls.find(call => call.method === 'PUT')!.body as {values: Record<string, unknown>; startChoice: Record<string, unknown>};
  assert.equal(body.values.allowAdvancedMediaAnalysis, undefined);assert.equal(body.values.wholeFileReads, true);
  assert.equal(body.startChoice.expectedAdvancedRevision, 0);
  held.resolve(receipt());await save;assert.equal(f.opening.getSnapshot().kind, 'saved');
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
});

test('retained input from draft A cannot edit or submit B; dirty consent is an independent ordinary save', async () => {
  const f = fixture();await f.opening.load();const a = f.draft();
  f.opening.changeConsent(a, true);const b = f.draft();
  f.opening.edit(a, {connections: 9});f.opening.select(b, b.profile.choiceDefinitions![1]!);
  assert.equal(f.draft(), b);assert.equal(b.choice, undefined);
  await f.opening.save(a, f.opening.getSnapshot());assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
  await f.opening.save(b, f.opening.getSnapshot());
  const body = f.calls.find(call => call.method === 'PUT')!.body as {values: Record<string, unknown>; startChoice?: unknown};
  assert.equal(body.startChoice, undefined);assert.equal(body.values.allowAdvancedMediaAnalysis, true);
});

test('an unedited consent fact is omitted from an ordinary save', async () => {
  const f = fixture();await f.opening.load();await f.opening.save(f.draft(), f.opening.getSnapshot());
  const body = f.calls.find(call => call.method === 'PUT')!.body as {values: Record<string, unknown>};
  assert.equal(Object.hasOwn(body.values, 'allowAdvancedMediaAnalysis'), false);
});

test('unknown retains the original SDK opening after the dispatch flight ends; repeated recovery is GET only', async () => {
  let reads = 0;
  const f = fixture({put: async () => {throw new TypeError('connection lost');}, recover: async () => {
    if (++reads === 1) throw Object.assign(new Error('not found yet'), {code: 'not_found'});return receipt();
  }});
  await f.opening.load();const a = f.draft();f.opening.select(a, a.profile.choiceDefinitions![1]!);await f.opening.save(f.draft(), f.opening.getSnapshot());
  const unknown = f.opening.getSnapshot();assert.equal(unknown.kind, 'unknown');if (unknown.kind !== 'unknown') return;
  f.opening.changeConsent(unknown.draft, true);await f.opening.save(unknown.draft, f.opening.getSnapshot());
  await f.opening.recover(unknown);const next = f.opening.getSnapshot();assert.equal(next.kind, 'unknown');
  if (next.kind !== 'unknown') return;assert.equal(next.result.intent, unknown.result.intent);
  await f.opening.recover(unknown);assert.equal(reads, 1, 'an old Check result cannot borrow the newer outcome');
  await f.opening.recover(next);assert.equal(f.opening.getSnapshot().kind, 'saved');
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);assert.equal(reads, 2);
});

test('confirmed acceptance plus failed refresh is terminal accepted-unavailable, never another PUT or recovery', async () => {
  const f = fixture({fresh: async () => {throw new TypeError('refresh offline');}});await f.opening.load();
  const a = f.draft();f.opening.select(a, a.profile.choiceDefinitions![1]!);const selected = f.draft();
  await f.opening.save(selected, f.opening.getSnapshot());const accepted = f.opening.getSnapshot();assert.equal(accepted.kind, 'accepted-unavailable');
  await f.opening.save(selected, f.opening.getSnapshot());assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
  if (accepted.kind === 'accepted-unavailable') assert.equal(accepted.result.settings, accepted.result.accepted.settings);
});

test('route/layout retirement aborts only this opening, suppresses late settlement, and never revives its commands', async () => {
  const held = deferred<unknown>(), f = fixture({put: () => held.promise});await f.opening.load();
  const a = f.draft();f.opening.select(a, a.profile.choiceDefinitions![1]!);const selected = f.draft();
  const save = f.opening.save(selected, f.opening.getSnapshot());await Promise.resolve();await Promise.resolve();
  const signal = f.calls.find(call => call.method === 'PUT')!.signal!;
  f.opening.retire();assert.equal(signal.aborted, true);held.resolve(receipt());await save;
  assert.equal(f.opening.getSnapshot().kind, 'retired');await f.opening.save(selected, f.opening.getSnapshot());
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
});

test('original viewer/source guard retirement before Save makes zero write I/O', async () => {
  const f = fixture();await f.opening.load();const a = f.draft();f.opening.select(a, a.profile.choiceDefinitions![1]!);
  const selected = f.draft();f.retireAuthority();await f.opening.save(selected, f.opening.getSnapshot());
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
});

test('an unavailable Advanced read supplies no invented pair and cannot offer an atomic choice', async () => {
  const f = fixture({advanced: async () => {throw new Error('unavailable');}});await f.opening.load();
  const draft = f.draft();assert.equal(draft.advanced, undefined);
  f.opening.select(draft, draft.profile.choiceDefinitions![1]!);assert.equal(f.draft(), draft);
});

test('reentrant publication and throwing view listeners cannot duplicate a command or erase its known success', async () => {
  const f = fixture();await f.opening.load();const a = f.draft();f.opening.select(a, a.profile.choiceDefinitions![1]!);
  const selected = f.draft();
  f.opening.subscribe(() => {void f.opening.save(selected, f.opening.getSnapshot());throw new Error('view listener failed');});
  await f.opening.save(selected, f.opening.getSnapshot());
  assert.equal(f.opening.getSnapshot().kind, 'saved');assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
});

test('ordinary known save followed by failed refresh also cannot offer a second write', async () => {
  const f = fixture({put: async () => ({scope: wire, receipt: {operationId: operation, accepted: true, removed: false, source}}),
    fresh: async () => {throw new TypeError('refresh offline');}});
  await f.opening.load();const draft = f.draft();await f.opening.save(draft, f.opening.getSnapshot());
  assert.equal(f.opening.getSnapshot().kind, 'saved-unavailable');await f.opening.save(draft, f.opening.getSnapshot());
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
});

test('a recorded choice is shown only with its exact current Advanced pair, never inferred from equal preset values', async () => {
  const legacy = fixture();await legacy.opening.load();assert.equal(legacy.opening.recorded(legacy.draft()), undefined);
  const stale = fixture({initial: parent()});await stale.opening.load();assert.equal(stale.opening.recorded(stale.draft()), undefined);
  const exact = fixture({initial: parent(), advanced: async () => ({scope: wire, settings: {
    sourceId: source.id, generation: '7', revision: 6, saved: true, cacheSupported: true, cacheApplied: true,
    cacheUsedBytes: 0, scanIntervalObserved: 0, values: advanced,
  }})});
  await exact.opening.load();const draft = exact.draft();
  assert.deepEqual(exact.opening.recorded(draft)?.comparison.changedFields, []);
  assert.equal(exact.opening.recorded(draft)?.definition, draft.profile.choiceDefinitions![1]);
  exact.opening.changeConsent(draft, true);const permission = exact.draft();
  assert.deepEqual(exact.opening.recorded(permission)?.comparison.changedFields, [], 'consent is not a counted setting');
  exact.opening.edit(permission, {connections: 9});assert.equal(exact.opening.recorded(exact.draft()), undefined, 'unsaved draft changes are not a new server comparison');
});

function customProfile(): SourceReadSettings {
  const saved = parent();
  return {...saved, values: {...saved.values, connections: 2, wholeFileReads: false, allowAdvancedMediaAnalysis: true},
    choiceBaseline: {...saved.choiceBaseline!, changedFields: ['connections']}};
}
function pairedAdvanced(values = advanced) {return {scope: wire, settings: {
  sourceId: source.id, generation: '7', revision: 6, saved: true, cacheSupported: true, cacheApplied: true,
  cacheUsedBytes: 0, scanIntervalObserved: 0, values,
}};}

test('Custom count comes from the coherent recorded difference; Reset all stages its exact version and saves atomically', async () => {
  const accepted = {...parent(3, 7), values: {...reading, wholeFileReads: false, allowAdvancedMediaAnalysis: true}};
  const f = fixture({initial: customProfile(), advanced: async () => pairedAdvanced(),
    put: async () => receipt(accepted), fresh: async () => ({scope: wire, settings: accepted})});
  await f.opening.load();const draft = f.draft(), recorded = f.opening.recorded(draft)!;
  assert.deepEqual(recorded.comparison.changedFields, ['connections']);
  assert.equal(recorded.definition, draft.profile.choiceDefinitions![1]);
  assert.equal(draft.consent, true);assert.equal(draft.values.wholeFileReads, false);
  f.opening.select(draft, recorded.definition);const reset = f.draft();
  assert.equal(reset.choice, recorded.definition);assert.equal(reset.values.connections, 4);
  assert.equal(reset.consent, true);assert.equal(reset.consentDirty, false);
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0, 'Reset is staged until the existing explicit Save');
  await f.opening.save(reset, f.opening.getSnapshot());assert.equal(f.opening.getSnapshot().kind, 'saved');
  const puts = f.calls.filter(call => call.method === 'PUT');assert.equal(puts.length, 1);
  assert.equal(puts[0]!.path.endsWith('/settings/read-profile'), true);
  const body = puts[0]!.body as {values: Record<string, unknown>; startChoice: {definitionRevision: number; expectedAdvancedRevision: number}};
  assert.equal(body.startChoice.definitionRevision, recorded.definition.definitionRevision);
  assert.equal(body.startChoice.expectedAdvancedRevision, 6);
  assert.equal(body.values.wholeFileReads, false);assert.equal(Object.hasOwn(body.values, 'allowAdvancedMediaAnalysis'), false);
});

test('a retained Reset from the previous rendered draft cannot reset a newer edit or dirty consent', async () => {
  const f = fixture({initial: customProfile(), advanced: async () => pairedAdvanced()});await f.opening.load();
  const a = f.draft(), reset = f.opening.recorded(a)!.definition;
  f.opening.edit(a, {connections: 3});const b = f.draft();f.opening.select(a, reset);
  assert.equal(f.draft(), b);assert.equal(f.draft().values.connections, 3);assert.equal(f.draft().choice, undefined);
  f.opening.changeConsent(b, false);const dirty = f.draft();f.opening.select(dirty, reset);
  assert.equal(f.draft(), dirty);assert.equal(dirty.consentDirty, true);assert.equal(dirty.choice, undefined);
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
});

test('a different offered definition version or a contradictory Advanced read withholds recorded reset/count', async () => {
  const old = customProfile();
  const newer = fixture({initial: {...old, choiceDefinitions: definitions.map(definition => ({...definition, definitionRevision: 2}))},
    advanced: async () => pairedAdvanced()});
  await newer.opening.load();assert.equal(newer.opening.recorded(newer.draft()), undefined);
  const mismatch = fixture({initial: old, advanced: async () => pairedAdvanced({...advanced, watchForChanges: true})});
  await mismatch.opening.load();assert.equal(mismatch.opening.recorded(mismatch.draft()), undefined);
  assert.equal(newer.calls.filter(call => call.method === 'PUT').length + mismatch.calls.filter(call => call.method === 'PUT').length, 0);
});

test('a retained Save cannot retry the same draft after a definite failure replaces its rendered state', async () => {
  const f = fixture({put: async () => {throw Object.assign(new Error('refused'), {code: 'invalid_request'});}});
  await f.opening.load();const a = f.draft();f.opening.select(a, a.profile.choiceDefinitions![1]!);
  const draft = f.draft(), rendered = f.opening.getSnapshot();
  await f.opening.save(draft, rendered);assert.equal(f.opening.getSnapshot().kind, 'failed');
  await f.opening.save(draft, rendered);
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1, 'old rendered callback cannot dispatch again');
});

test('a per-reading-field Reset stages only its recorded value and a stale marker cannot reset a later draft', async () => {
  const f = fixture({initial: customProfile(), advanced: async () => pairedAdvanced()});await f.opening.load();
  const a = f.draft(), rendered = f.opening.getSnapshot();f.opening.resetField(a, rendered, 'connections');
  const b = f.draft();assert.equal(b.values.connections, reading.connections);assert.equal(b.choice, undefined);
  assert.equal(b.consent, true);assert.equal(b.values.wholeFileReads, false);
  f.opening.resetField(a, rendered, 'requestsPerSecond');assert.equal(f.draft(), b);assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
});

test('a per-Advanced-field Reset uses the original Advanced CAS and never a split preset apply or consent write', async () => {
  const actual = {...advanced, scanOnStart: true}, initial = parent();
  const custom = {...initial, choiceBaseline: {...initial.choiceBaseline!, currentAdvanced: actual, changedFields: ['scanOnStart'] as const}};
  const f = fixture({initial: custom, advanced: async () => pairedAdvanced(actual)});await f.opening.load();
  const a = f.draft();assert.deepEqual(f.opening.recorded(a)?.comparison.changedFields, ['scanOnStart']);
  f.opening.resetField(a, f.opening.getSnapshot(), 'scanOnStart');const b = f.draft();
  assert.equal(b.advancedValues?.scanOnStart, false);assert.equal(b.advancedDirty, true);assert.equal(f.opening.recorded(b), undefined);
  const state = f.opening.getSnapshot();await f.opening.save(b, state);assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
  await f.opening.saveAdvanced(b, state);const writes = f.calls.filter(call => call.method === 'PUT');
  assert.equal(writes.length, 1);assert.equal(writes[0].path.endsWith('/settings/advanced'), true);
  assert.deepEqual(writes[0].body, {expectedGeneration: '7', expectedRevision: 6, values: advanced});
  assert.equal(f.opening.getSnapshot().kind, 'saved');
});

test('dirty permission or Reading drafts cannot be silently mixed into an Advanced save', async () => {
  for (const mode of ['consent', 'reading'] as const) {
    const f = fixture({initial: parent(), advanced: async () => pairedAdvanced()});await f.opening.load();const a = f.draft();
    if (mode === 'consent') f.opening.changeConsent(a, true);else f.opening.edit(a, {connections: 8});
    const b = f.draft();f.opening.editAdvanced(b, {...advanced, watchForChanges: true});assert.equal(f.draft(), b);
    await f.opening.saveAdvanced(b, f.opening.getSnapshot());assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
  }
});

test('an Advanced Save is single-flight, exact-render-bound and retired replies cannot publish success', async () => {
  const held = deferred<unknown>(), f = fixture({advancedPut: () => held.promise});await f.opening.load();const a = f.draft();
  f.opening.editAdvanced(a, {...advanced, watchForChanges: true});const b = f.draft(), rendered = f.opening.getSnapshot();
  const save = f.opening.saveAdvanced(b, rendered);await f.opening.saveAdvanced(b, rendered);f.opening.retire();
  held.resolve(pairedAdvanced({...advanced, watchForChanges: true}));await save;
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);assert.equal(f.opening.getSnapshot().kind, 'retired');
});

test('choice Advanced preview preserves inapplicable original values; it grants no consent', async () => {
  const original = {...profile(), choiceApplicableFields: sourceChoiceFields.filter(field => field !== 'watchForChanges')};
  const f = fixture({initial: original, advanced: async () => pairedAdvanced({...advanced, watchForChanges: true})});await f.opening.load();
  const a = f.draft();f.opening.select(a, a.profile.choiceDefinitions![1]);const b = f.draft();
  assert.equal(f.opening.previewAdvanced(b)?.watchForChanges, true);assert.equal(b.consent, false);assert.equal(b.consentDirty, false);
});

test('retired and successor openings cannot adopt each other’s drafts or late receipts', async () => {
  const oldReply = deferred<unknown>(), nextReply = deferred<unknown>();
  const oldSent = deferred<void>(), nextSent = deferred<void>();
  const old = fixture({put: () => {oldSent.resolve();return oldReply.promise;}}),
    next = fixture({put: () => {nextSent.resolve();return nextReply.promise;}});
  await old.opening.load();const oldDraft = old.draft();
  const oldSave = old.opening.save(oldDraft, old.opening.getSnapshot());
  await oldSent.promise;old.opening.retire();
  assert.equal(old.calls.filter(call => call.method === 'PUT').length, 1, 'retirement follows actual original dispatch');
  await next.opening.load();const nextDraft = next.draft();
  next.opening.changeConsent(oldDraft, true);next.opening.edit(oldDraft, {connections: 9});
  assert.equal(next.draft(), nextDraft);
  const nextSave = next.opening.save(nextDraft, next.opening.getSnapshot());
  await nextSent.promise;
  oldReply.resolve(receipt());await oldSave;
  assert.equal(old.opening.getSnapshot().kind, 'retired');
  assert.equal(next.opening.getSnapshot().kind, 'saving', 'old completion cannot release a successor flight');
  next.opening.changeConsent(nextDraft, true);
  await next.opening.save(nextDraft, next.opening.getSnapshot());
  assert.equal(next.calls.filter(call => call.method === 'PUT').length, 1);
  nextReply.resolve(receipt());await nextSave;
  assert.equal(next.opening.getSnapshot().kind, 'saved');
  old.opening.retire();next.opening.retire();
});

test('unknown consent stays absent and every offered choice preserves independent consent and whole-file policy', async () => {
  const unknown = profile();const {allowAdvancedMediaAnalysis: _consent, ...withoutConsent} = unknown.values;
  const f = fixture({initial: {...unknown, values: withoutConsent}});await f.opening.load();const original = f.draft();
  assert.equal(original.consent, undefined);f.opening.changeConsent(original, true);assert.equal(f.draft(), original);
  await f.opening.save(original, f.opening.getSnapshot());
  const body = f.calls.find(call => call.method === 'PUT')!.body as {values: Record<string, unknown>};
  assert.equal(Object.hasOwn(body.values, 'allowAdvancedMediaAnalysis'), false);f.opening.retire();
  for (const name of ['once', 'list', 'everything', 'ask']) {
    for (const consent of [false, true]) {
      const initial = profile();
      const next = fixture({initial: {...initial, values: {...initial.values, wholeFileReads: true, allowAdvancedMediaAnalysis: consent}}});
      await next.opening.load();const a = next.draft();
      next.opening.select(a, a.profile.choiceDefinitions!.find(choice => choice.name === name)!);
      const selected = next.draft();
      assert.equal(selected.consent, consent);assert.equal(selected.consentDirty, false);
      assert.equal(selected.values.wholeFileReads, true);
      assert.equal(Object.hasOwn(selected.values, 'allowAdvancedMediaAnalysis'), false);
      next.opening.changeConsent(a, !consent);assert.equal(next.draft(), selected, 'stale checkbox cannot edit the successor record');
      await next.opening.save(selected, next.opening.getSnapshot());
      const write = next.calls.find(call => call.method === 'PUT')!.body as {values: Record<string, unknown>};
      assert.equal(write.values.wholeFileReads, true);assert.equal(Object.hasOwn(write.values, 'allowAdvancedMediaAnalysis'), false);
      next.opening.retire();
    }
  }
});

test('explicit consent changes preserve both false and true while retired guards refuse edits', async () => {
  for (const consent of [false, true]) {
    const initial = profile(), f = fixture({initial: {...initial, values: {...initial.values, allowAdvancedMediaAnalysis: consent}}});
    await f.opening.load();const a = f.draft();f.opening.changeConsent(a, !consent);const changed = f.draft();
    f.opening.changeConsent(a, consent);assert.equal(f.draft(), changed);
    await f.opening.save(changed, f.opening.getSnapshot());
    const body = f.calls.find(call => call.method === 'PUT')!.body as {values: Record<string, unknown>};
    assert.equal(body.values.allowAdvancedMediaAnalysis, !consent);assert.equal(body.values.wholeFileReads, true);
    f.opening.retire();f.opening.changeConsent(changed, consent);assert.equal(f.opening.getSnapshot().kind, 'retired');
  }
  const broken = new ReadingProfileOpening({} as never, source, () => {throw new Error('retired');}, () => true);
  assert.equal(broken.current(), false);
});
