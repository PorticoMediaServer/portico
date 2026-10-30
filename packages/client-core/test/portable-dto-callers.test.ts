import test from 'node:test';
import assert from 'node:assert/strict';
import {ReadingAnnotationService} from '../src/reading-annotation-service.ts';
import type {ReadingAnnotationContent, ReadingAnnotationMutation} from '../src/reading-annotations.ts';
import {SettingsChannelActionsSource} from '../src/server-admin/channel-actions-source.ts';
import {SettingsChannelEditorSource} from '../src/server-admin/channel-editor-source.ts';
import type {SettingsLibraryChannelsSource} from '../src/server-admin/channels-source.ts';
import {BlockPresetsSource, type BlockPreset, type BlockPresetBody} from '../src/server-admin/block-presets-source.ts';
import {parseLibraryChannel} from '../src/library-channels.ts';

async function withoutStructuredClone(run: () => Promise<void>) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'structuredClone');
  delete (globalThis as {structuredClone?: unknown}).structuredClone;
  try { await run(); }
  finally { if (descriptor) Object.defineProperty(globalThis, 'structuredClone', descriptor); }
}

test('annotation write captures its actual mutation and settles without global structuredClone', () => withoutStructuredClone(async () => {
  const life = new AbortController();
  const draft: ReadingAnnotationContent = {id: 'mark', kind: 'bookmark', location: {format: 'pdf', pageIndex: 2, progression: .2}, text: '', note: 'Original note', colour: ''};
  let revision = 1, release!: () => void;
  const held = new Promise<void>(resolve => {release = resolve;});
  const sent: ReadingAnnotationMutation[] = [];
  const service = new ReadingAnnotationService({
    manifest: {sourceId: 'source', revision: 'source-r1', canAnnotate: true}, signal: life.signal,
    annotations: async () => ({sourceId: 'source', sourceRevision: 'source-r1', revision, entries: [], nextCursor: ''}),
    mutateAnnotation: async mutation => {sent.push(mutation); await held; revision++; return {revision};},
  }, () => true);
  try {
    await service.load();
    const write = service.write(draft, {revision: 1, signal: life.signal, isCurrent: () => true});
    assert.equal(sent.length, 1, 'the actual mutation port is reached');
    assert.equal(sent[0].expectedRevision, 1); assert.equal(sent[0].operation, 'upsert');
    assert('location' in sent[0]); assert.notEqual(sent[0].location, draft.location);
    Object.assign(draft.location, {pageIndex: 9}); Object.assign(draft, {note: 'Changed after dispatch'});
    assert.equal(sent[0].location.pageIndex, 2); assert.equal(sent[0].note, 'Original note');
    release(); assert.equal(await write, true); assert.equal(service.getSnapshot().revision, 2);
    assert.equal(service.getSnapshot().saving, false); assert.equal(service.getSnapshot().error, undefined);
  } finally {release(); service.dispose(); life.abort();}
}));

const channel = () => parseLibraryChannel({
  config: {protocolVersion: '1.0', id: 'c1', name: 'Channel', description: '', enabled: true, position: 0, timezone: 'UTC', seed: 'seed', defaultRuleId: 'r1', viewerAccess: 'owner-only', logoItemId: '', templateId: '',
    quality: {mode: 'automatic', maxBitrate: 8000000, maxHeight: 1080, allowLossy: true, allowHdrToSdr: true},
    overlay: {enabled: false, corner: 'br', sizePercent: 10, insetPercent: 5, treatment: 'none'},
    rules: [{id: 'r1', name: 'Rule', query: {libraryIds: ['library'], kinds: ['movie'], recentDays: 0, limit: 10, order: 'title'}, mode: 'sequential', episodeMode: 'none', exhaustion: 'loop', deduplicationWindow: 0, maxConsecutive: 1}], blocks: []},
  revision: 1, state: 'ok', healthCode: '', generation: 'g1', generatedThrough: '', candidateCount: 0, unresolvedDurationCount: 0, replacementBoundary: '',
});

test('actual channel editor clones the offered config, change and save with no global structuredClone', () => withoutStructuredClone(async () => {
  const original = channel(); let reloads = 0, invalidations = 0;
  const reader = {getSnapshot: () => ({phase: 'ready', channels: [original]}), load: async () => {reloads++;}, invalidateQuiet: () => {invalidations++;}} as unknown as SettingsLibraryChannelsSource;
  const sent: {path: string; method?: string; body?: unknown}[] = [];
  const editor = new SettingsChannelEditorSource({request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    sent.push({path, method, body});
    const mutation = body as {config: typeof original.config; expectedRevision: number; requestId: string};
    assert.equal(mutation.expectedRevision, 1); assert.equal(mutation.requestId, 'operation-original');
    assert.equal(mutation.config.name, 'Changed channel'); assert.deepEqual(mutation.config.rules[0].query.libraryIds, ['library', 'extra']);
    return {serverId: 'server', protocolVersion: '1.0', channel: {...original, config: mutation.config, revision: 2}} as T;
  }}, 'server', reader, () => true, error => {throw error;}, () => 'operation-original');
  try {
    editor.edit(original); const opened = editor.getSnapshot().config!;
    assert.notEqual(opened, original.config); assert.notEqual(opened.rules, original.config.rules);
    editor.change(config => ({...config, name: 'Changed channel', rules: config.rules.map(rule => ({...rule, query: {...rule.query, libraryIds: [...rule.query.libraryIds, 'extra']}}))}));
    assert.equal(original.config.name, 'Channel'); assert.deepEqual(original.config.rules[0].query.libraryIds, ['library']);
    const saved = await editor.save(); assert.equal(saved?.revision, 2); assert.equal(sent.length, 1);
    assert.equal(sent[0].method, 'POST'); assert.equal(sent[0].path, '/v1/admin/library-channels');
    assert.equal(editor.getSnapshot().phase, 'closed'); assert.equal(reloads, 1); assert.equal(invalidations, 1);
  } finally {editor.dispose();}
}));

test('actual block preset save captures the original nested draft and publishes the receipt without global structuredClone', () => withoutStructuredClone(async () => {
  const draft: BlockPresetBody = {libraryId: 'library', description: 'Original', order: 'sequential', durationMinutes: 60, startMinute: -1, days: ['monday'], criteria: [{variant:'predicate',field: 'genre', operator: 'includes', values: ['Drama']}]};
  let stored: BlockPreset | undefined, captured: BlockPresetBody | undefined, release!: () => void;
  const held = new Promise<void>(resolve => {release = resolve;}); let posts = 0;
  const source = new BlockPresetsSource({request: async <T>(path: string, method?: string, raw?: unknown): Promise<T> => {
    if(path==='/v1/libraries/library/browse-capabilities?pivot=')return {library:{id:'library',name:'Movies',kind:'movie',defaultView:'discover',pinned:false},pivots:[],fields:[{id:'genre',labelKey:'browse.field.genre',type:'identity-set',operators:['contains-any'],controlHint:'facet-multi-select',complexity:'quick',cost:'indexed-join',applicableKinds:['movie']}],sorts:[],quickFilters:[],surfaces:[],queryLimits:{maximumDepth:5,maximumClauses:40,maximumBytes:65536,maximumSorts:3,defaultLimit:40,maximumLimit:100,cursorTtlMs:1800000}} as T;
    if (method === 'POST' || method === 'PUT') {
      posts++; const mutation = raw as {body: BlockPresetBody; name: string; expectedRevision: number; operationId: string};
      assert.equal(mutation.expectedRevision, stored?.revision??0); assert.equal(mutation.operationId, 'operation-original');
      captured = mutation.body; await held;
      stored = {id: 'preset', name: mutation.name, body: captured, revision: mutation.expectedRevision+1, createdAt: '2026-10-27T00:00:00Z', updatedAt: '2026-10-27T00:00:00Z'};
      return {serverId: 'server', protocolVersion: '1.0', result: stored} as T;
    }
    return {serverId: 'server', protocolVersion: '1.0', result: {items: stored ? [stored] : [], nextCursor: '', enumerations: {applicationOrder: ['shuffle', 'sequential', 'recent']}}} as T;
  }}, 'server', () => true, error => {throw error;}, () => 'operation-original');
  try {
    await source.load(); const saving = source.save(undefined, 'Original preset', draft);
    assert.equal(posts, 1); assert(captured); assert.notEqual(captured, draft); assert.notEqual(captured.criteria[0].values, draft.criteria[0].values);
    draft.criteria[0].values[0] = 'Comedy'; draft.days.push('tuesday');
    assert.deepEqual(captured.criteria[0].values, ['Drama']); assert.deepEqual(captured.days, ['monday']);
    release(); assert.equal(await saving, true); assert.equal(source.getSnapshot().items[0].name, 'Original preset');
    assert.deepEqual(source.getSnapshot().items[0].body.criteria[0].values, ['Drama']); assert.equal(source.getSnapshot().error, undefined);
    const record=source.getSnapshot().items[0],config=channel().config;
    config.blocks.push({id:'block',weekdays:[1],startMinute:0,endMinute:60,priority:0,ruleId:'r1',fallbackRuleId:'',anchor:'channel-cursor',overrun:'finish'});
    const caller=new AbortController(),applied=await source.apply(record,config,config.blocks[0],caller.signal,()=>true);
    assert(applied);assert.notEqual(applied,config);assert.equal(applied.rules.length,2);assert.equal(config.rules.length,1);
    assert.notEqual(applied.blocks[0].ruleId,'r1');assert.equal(config.blocks[0].ruleId,'r1');
    assert.deepEqual(applied.rules[1].query.filter,{variant:'group',all:[{variant:'predicate',field:'genre',operator:'contains-any',value:['Drama']}]});
    caller.abort();assert.equal(await source.apply(record,config,config.blocks[0],caller.signal,()=>true),undefined);
    assert.equal(await source.save(record,'Edited preset',draft),true);assert.equal(posts,2);
    assert.equal(source.getSnapshot().items[0].revision,2);assert.equal(source.getSnapshot().items[0].name,'Edited preset');
    assert.equal(await source.save(record,'Stale original preset',draft),false,'retired record cannot mutate its successor');assert.equal(posts,2);
  } finally {release(); source.dispose();}
}));


test('channel toggle retains original operation and revision with global structuredClone absent and refuses stale rows',()=>withoutStructuredClone(async()=>{
 const original=channel(),snapshot={phase:'ready',channels:[original]};let reloads=0,calls=0;
 const reader={getSnapshot:()=>snapshot,load:async()=>{reloads++;}} as unknown as SettingsLibraryChannelsSource;
 const actions=new SettingsChannelActionsSource({request:async<T>(path:string,method?:string,raw?:unknown):Promise<T>=>{
  calls++;assert.equal(path,'/v1/admin/library-channels');assert.equal(method,'POST');
  const mutation=raw as {config:typeof original.config;expectedRevision:number;requestId:string};
  assert.equal(mutation.config.enabled,false);assert.equal(mutation.expectedRevision,1);assert.equal(mutation.requestId,'original-toggle');assert.notEqual(mutation.config,original.config);
  return {serverId:'server',protocolVersion:'1.0',channel:{...original,config:mutation.config,revision:2}} as T;
 }},'server',reader,()=>true,error=>{throw error;},()=> 'original-toggle');
 try{
  assert.equal(await actions.perform({...original},'toggle'),false);assert.equal(calls,0);
  assert.equal(await actions.perform(original,'toggle'),true);assert.equal(calls,1);assert.equal(reloads,1);assert.equal(original.config.enabled,true);
  snapshot.channels=[channel()];assert.equal(await actions.perform(original,'toggle'),false);assert.equal(calls,1);
 }finally{actions.dispose();}
}));
