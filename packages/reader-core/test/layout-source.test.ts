import assert from 'node:assert/strict';
import {test} from 'node:test';
import {planFixedLayout, spreadFits, creaseSpread, fixedVisualPanes} from '../src/layout.ts';
import {epubDisplay, type EpubDisplayPreferences} from '../src/epub/display.ts';
import {readRange, sourceFromBlob, MAX_READER_RANGE_BYTES, validateSource} from '../src/source.ts';
const base = {format: 'cbz' as const, viewport: {width: 1180, height: 820}, pageCount: 6, twoPages: true,
  firstPageAlone: true, direction: 'ltr' as const, fit: 'auto' as const};
test('cover offset, RTL visual ordering, next/previous bounds and narrow/vertical singles', () => {
  assert.deepEqual(planFixedLayout({...base, page: 0}).pages, [0]);
  assert.deepEqual(planFixedLayout({...base, page: 2}).pages, [1,2]);
  assert.deepEqual(planFixedLayout({...base, page: 5}).pages, [5]);
  const rtl = planFixedLayout({...base, page: 3, direction: 'rtl'});
  assert.deepEqual(rtl.pages, [3,4]); assert.deepEqual(rtl.visualPages, [4,3]);
  assert.equal(rtl.nextPage, 5); assert.equal(rtl.previousPage, 1);
  assert.equal(planFixedLayout({...base, page: 5}).nextPage, null);
  assert.equal(planFixedLayout({...base, page: 0}).previousPage, null);
  assert.equal(planFixedLayout({...base, page: 1}).previousPage, 0);
  assert.deepEqual(planFixedLayout({...base, page: 2, firstPageAlone: false}).pages, [2,3]);
  assert.deepEqual(planFixedLayout({...base, page: 2, direction: 'vertical'}).pages, [2]);
  assert.deepEqual(planFixedLayout({...base, page: 2, viewport: {width: 820, height: 1180}}).pages, [2]);
  assert.equal(planFixedLayout({...base, page: 2, viewport: {width: 390, height: 844}}).fit, 'width');
  assert.equal(spreadFits({width: 1024, height: 800}), true);
  assert.equal(spreadFits({width: 1023, height: 800}), false);
  assert.throws(() => planFixedLayout({...base, page: 6}));
});
test('a reported book fold opens a spread only inside the drawn gutter', () => {
  const fold = {width: 890, height: 626, crease: {left: 445, right: 445}};
  assert.equal(creaseSpread(fold), true);
  assert.deepEqual(planFixedLayout({...base, viewport: fold, page: 2}).pages, [1, 2]);
  assert.equal(creaseSpread({...fold, crease: {left: 409, right: 481}}), true);
  assert.equal(creaseSpread({...fold, crease: {left: 390, right: 510}}), false);
  assert.equal(creaseSpread({...fold, crease: {left: 430, right: 460}}), true);
  assert.equal(creaseSpread({...fold, width: 590, crease: {left: 295, right: 295}}), false);
  assert.equal(creaseSpread({width: 1000, height: 700, crease: {left: 500, right: 500}}), true);
  assert.equal(spreadFits({width: 1000, height: 700}), false);
  assert.deepEqual(planFixedLayout({...base, viewport: fold, page: 2, direction: 'vertical'}).pages, [2]);
});
test('fixed-page pages map to their own pane, including the planner RTL order', () => {
  const fold = {width: 890, height: 626, crease: {left: 445, right: 445}};
  const ltr = planFixedLayout({...base, viewport: fold, page: 2});
  assert.deepEqual(ltr.visualPages, [1, 2]);
  assert.deepEqual(fixedVisualPanes(fold, ltr.visualPages, 'ltr'), [
    {page: 1, pane: {x: 0, width: 445, height: 626}}, {page: 2, pane: {x: 445, width: 445, height: 626}}]);
  const rtl = planFixedLayout({...base, viewport: fold, page: 3, direction: 'rtl'});
  assert.deepEqual(rtl.visualPages, [4, 3]);
  // The planner already reversed reading order; the first visual page is physically left.
  assert.deepEqual(fixedVisualPanes(fold, rtl.visualPages, 'rtl'), [
    {page: 4, pane: {x: 0, width: 445, height: 626}}, {page: 3, pane: {x: 445, width: 445, height: 626}}]);
  assert.deepEqual(fixedVisualPanes(fold, [0], 'ltr', 'cover'), [{page: 0, pane: {x: 445, width: 445, height: 626}}]);
  assert.deepEqual(fixedVisualPanes(fold, [5], 'ltr', 'last'), [{page: 5, pane: {x: 0, width: 445, height: 626}}]);
  assert.deepEqual(fixedVisualPanes(fold, [0], 'rtl', 'cover'), [{page: 0, pane: {x: 0, width: 445, height: 626}}]);
  assert.deepEqual(fixedVisualPanes(fold, [5], 'rtl', 'last'), [{page: 5, pane: {x: 445, width: 445, height: 626}}]);
  assert.deepEqual(fixedVisualPanes({...fold, crease: {left: 390, right: 510}}, [1, 2], 'ltr'), []);
  assert.deepEqual(fixedVisualPanes({width: 890, height: 626}, [1, 2], 'ltr'), []);
});
test('a book fold keeps the drawn gutter, including on an otherwise wide window', () => {
  const preferences: EpubDisplayPreferences = {theme: 'dark', font: 'literata', textSize: 5, lineSpacing: 'normal', margins: 'normal',
    alignment: 'justified', layout: 'pages', twoPages: true, hyphenation: true, tapToTurn: true, highlightColour: 'yellow'};
  const fonts = {literata: 'data:font/woff2;base64,AA==', manrope: 'data:font/woff2;base64,AA=='};
  const fold = epubDisplay(preferences, {width: 890, height: 626, crease: {left: 445, right: 445}}, fonts);
  assert.equal(fold.columns, 2);
  const bad = epubDisplay(preferences, {width: 890, height: 626, crease: {left: 390, right: 510}}, fonts);
  assert.equal(bad.columns, 1);
  assert.equal(epubDisplay(preferences, {width: 890, height: 700}, fonts).columns, 1);
  const wideFold = epubDisplay(preferences, {width: 1180, height: 820, crease: {left: 590, right: 590}}, fonts);
  const wideFlat = epubDisplay(preferences, {width: 1180, height: 820}, fonts);
  assert.equal(wideFold.columns, 2); assert.equal(wideFlat.columns, 2);
  assert.ok(wideFold.gap < wideFlat.gap);
});
test('range source enforces exact length, bounds, per-read budget and cancellation', async () => {
  const signal = new AbortController().signal;
  const source = sourceFromBlob(new Blob([new Uint8Array([2,3,4,5])]), 'local', 'immutable-fixture-v1');
  assert.deepEqual([...await readRange(source, {start: 1, endExclusive: 3}, signal)], [3,4]);
  await assert.rejects(readRange(source, {start: -1, endExclusive: 1}, signal));
  await assert.rejects(readRange(source, {start: 0, endExclusive: 5}, signal));
  await assert.rejects(readRange({...source, read: async () => new Uint8Array()}, {start: 1, endExclusive: 2}, signal));
  await assert.rejects(readRange({...source, size: MAX_READER_RANGE_BYTES+1}, {start: 0, endExclusive: MAX_READER_RANGE_BYTES+1}, signal));
  const abort = new AbortController(); abort.abort();
  await assert.rejects(readRange(source, {start: 0, endExclusive: 1}, abort.signal));
  let release!: () => void; const delayed = {...source, async read() {await new Promise<void>(r => release = r); return new Uint8Array([2]);}};
  const interrupted = new AbortController(); const reading = readRange(delayed, {start: 0, endExclusive: 1}, interrupted.signal);
  interrupted.abort(); release(); await assert.rejects(reading);
});
test('range source rejects identity changes while a read is pending',async()=>{
  for(const field of ['id','revision','size'] as const){
    let resolve!: (bytes: Uint8Array) => void, started!: () => void;
    const entered=new Promise<void>(done=>started=done);
    const source={id:'a',revision:'r1',size:4,async read(){started();return new Promise<Uint8Array>(done=>resolve=done);}};
    const pending=readRange(source,{start:0,endExclusive:4},new AbortController().signal);
    await entered;(source as unknown as Record<string,unknown>)[field]=field==='size'?5:'changed';
    resolve(new Uint8Array([1,2,3,4]));
    await assert.rejects(pending,{code:'invalid_source'});
  }
  const stable={id:'a',revision:'r1',size:4,async read(){return new Uint8Array([1,2,3,4]);}};
  assert.deepEqual([...await readRange(stable,{start:0,endExclusive:4},new AbortController().signal)],[1,2,3,4]);
});
test('drawn compact sideways PDF/manga spreads differ from LTR comic width single', () => {
  const viewport = {width:844,height:390};
  assert.deepEqual(planFixedLayout({...base,viewport,page:2,direction:'rtl'}).pages,[1,2]);
  assert.deepEqual(planFixedLayout({...base,viewport,page:2,format:'pdf'}).pages,[1,2]);
  const comic = planFixedLayout({...base,viewport,page:2});
  assert.deepEqual(comic.pages,[2]); assert.equal(comic.fit,'width');
  assert.deepEqual(planFixedLayout({...base,viewport,page:2,twoPages:false,direction:'rtl'}).pages,[2]);
});

test('sequential source is genuine from-start access and cannot masquerade as ranges',async()=>{
  const source={id:'sequential-publication',revision:'actual-current-revision',size:3,sequential:{async open(signal:AbortSignal){
    if(signal.aborted)throw new Error('aborted');return new ReadableStream<Uint8Array>({start(c){c.enqueue(new Uint8Array([1,2,3]));c.close();}});
  }}};
  validateSource(source);
  const reader=(await source.sequential.open(new AbortController().signal)).getReader();assert.deepEqual([...(await reader.read()).value!],[1,2,3]);
  await assert.rejects(readRange(source,{start:0,endExclusive:1},new AbortController().signal),{code:'invalid_source'});
  assert.throws(()=>validateSource({...source,read:async()=>new Uint8Array([1])}),{code:'invalid_source'});
  const cancelled=new AbortController();cancelled.abort();await assert.rejects(source.sequential.open(cancelled.signal));
});
