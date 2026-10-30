import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {parsePreferenceSnapshot, readingPreferences, readingPreferencesPatch,
  type PreferenceSnapshot, type ReadingPreferences, type ReadingPreferencesChange} from '@portico/client-core';
import {createReadingController} from '../src/index.ts';
import type {ReaderAdapter, ReaderMount, ReaderView} from '../src/index.ts';
import {epubDisplay} from '../src/epub/display.ts';

// Actual recorded server bootstrap; the registry remains the only defaults.
const recorded=JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json',import.meta.url),'utf8')).response.preferences;
const initial=()=>parsePreferenceSnapshot(recorded);
const source={id:'snapshot-controller-fixture',revision:'revision-1',size:1};
const viewport={width:1440,height:900};
const container={} as HTMLElement;
const fonts={literata:'blob:trusted-literata',manrope:'blob:trusted-manrope'};

/** Controlled confirmed-response fixture only, not an HTTP save/effect claim.
 * Test construction uses the canonical Settings patch to avoid a second map. */
function confirmed(before:PreferenceSnapshot,change:ReadingPreferencesChange):PreferenceSnapshot {
  const patch=readingPreferencesPatch(before,change),effective={...before.effective},effectiveSource={...before.effectiveSource};
  const document=before.documents.find(d=>d.scope==='profile-server')!;
  const values={...document.values};
  for(const [key,value]of Object.entries(patch)) {
    if(value===null){delete values[key];effective[key]=before.registry.fields.find(f=>f.key===key)!.default;effectiveSource[key]='default';}
    else{values[key]=value;effective[key]=value;effectiveSource[key]='profile-server';}
  }
  return parsePreferenceSnapshot({...before,effective,effectiveSource,documents:before.documents.map(d=>d.scope==='profile-server'
    ?{...d,values,revision:d.revision+1,activeRevision:d.revision+1}:d)});
}
function fixture(snapshot=initial()) {
  const seen:Readonly<ReadingPreferences>[]=[],styles:ReturnType<typeof epubDisplay>[]=[];
  let mount!:ReaderMount<ReadingPreferences>,disposed=0;
  const view:ReaderView<ReadingPreferences>={async navigate(){},async resize(){},getLocation(){return null;},dispose(){disposed++;},
    async applyPreferences(preferences){seen.push(preferences);styles.push(epubDisplay(preferences,viewport,fonts));}};
  const adapter:ReaderAdapter<ReadingPreferences>={format:'epub',async open(){return {id:source.id,revision:source.revision,format:'epub',metadata:{},toc:[],
    async mount(options){mount=options;return view;},async *search(){},dispose(){}};}};
  const reader=createReadingController({adapters:[adapter],preferenceSnapshot:snapshot});
  return {reader,seen,styles,get mount(){return mount;},get disposed(){return disposed;}};
}

test('confirmed snapshot facade reads the actual fifteen-row projection at each book open',async()=>{
  const before=initial();let reads=0;
  const observed:PreferenceSnapshot={...before,get effective(){reads++;return before.effective;}};
  const f=fixture(observed),constructed=reads;
  assert.equal(Object.keys(f.reader.getSnapshot().preferences).length,15);
  await f.reader.open({source,format:'epub',container,viewport});
  assert.ok(reads>constructed);
  assert.deepEqual(f.mount.preferences,readingPreferences(before));
  assert.deepEqual(f.seen.at(-1),readingPreferences(before));
  const opened=reads;await f.reader.close();await f.reader.open({source,format:'epub',container,viewport});assert.ok(reads>opened);
  await f.reader.close();assert.equal(f.disposed,2);
});

test('all fifteen confirmed fields reach one controller while book-local overrides retain precedence and reset on close',async()=>{
  const before=initial(),original=JSON.stringify(before),f=fixture(before);
  await f.reader.open({source,format:'epub',container,viewport});
  await f.reader.setOverride({font:'publisher',textSize:7,comicDirection:'rtl'});
  const next=confirmed(before,{theme:'sepia',font:'manrope',textSize:2,lineSpacing:'loose',margins:'wide',alignment:'left',layout:'scroll',
    twoPages:false,tapToTurn:false,hyphenation:false,highlightColour:'pink',pdfView:'width',comicFit:'height',comicDirection:'vertical',firstPageAlone:false});
  await f.reader.updatePreferenceSnapshot(next);
  const expected={...readingPreferences(next),font:'publisher',textSize:7,comicDirection:'rtl'};
  assert.deepEqual(f.reader.getSnapshot().preferences,expected);
  assert.deepEqual(f.seen.at(-1),expected);
  assert.deepEqual(f.reader.getSnapshot().overriddenKeys,['font','textSize','comicDirection']);
  const style=f.styles.at(-1)!;
  assert.equal(style.background,'#f1e6d1');assert.equal(style.flow,'scrolled');assert.equal(style.columns,1);
  assert.match(style.css,/font-size:26px/);assert.match(style.css,/text-align:left/);assert.match(style.css,/hyphens:none/);
  await f.reader.clearOverrides(['font']);assert.equal(f.reader.getSnapshot().preferences.font,'manrope');
  await f.reader.close();assert.deepEqual(f.reader.getSnapshot().preferences,readingPreferences(next));
  await f.reader.open({source,format:'epub',container,viewport});assert.deepEqual(f.mount.preferences,readingPreferences(next));
  assert.deepEqual(f.reader.getSnapshot().overriddenKeys,[]);assert.equal(JSON.stringify(before),original);
  await f.reader.close();
});

test('confirmed updates during mounting and while closed stay live without a Settings writer',async()=>{
  const before=initial(),f=fixture(before);let release!:()=>void,entered!:()=>void;
  const started=new Promise<void>(resolve=>entered=resolve),waiting=new Promise<void>(resolve=>release=resolve);
  const adapter:ReaderAdapter<ReadingPreferences>={format:'epub',async open(){entered();await waiting;return {id:source.id,revision:source.revision,format:'epub',metadata:{},toc:[],
    async mount(options){assert.equal(options.preferences.theme,'light');return {async navigate(){},async applyPreferences(){},async resize(){},getLocation(){return null;},dispose(){}};},async *search(){},dispose(){}};}};
  const reader=createReadingController({adapters:[adapter],preferenceSnapshot:before});
  const opening=reader.open({source,format:'epub',container,viewport});await started;
  const next=confirmed(before,{theme:'light'});await reader.updatePreferenceSnapshot(next);release();await opening;
  assert.equal(reader.getSnapshot().preferences.theme,'light');await reader.close();
  await f.reader.updatePreferenceSnapshot(next);assert.equal(f.reader.getSnapshot().status,'closed');
  await f.reader.open({source,format:'epub',container,viewport});assert.equal(f.mount.preferences.theme,'light');await f.reader.close();
});

test('a missing required canonical registry field rejects intake before replacing the last confirmed projection',async()=>{
  const before=initial(),f=fixture(before),prior=f.reader.getSnapshot();
  const missing={...before,effective:{},registry:{...before.registry,fields:[]}};
  assert.throws(()=>f.reader.updatePreferenceSnapshot(missing),{code:'invalid_preferences'});
  assert.equal(f.reader.getSnapshot(),prior);
  await f.reader.open({source,format:'epub',container,viewport});assert.deepEqual(f.mount.preferences,readingPreferences(before));await f.reader.close();
});
