import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {fileURLToPath} from 'node:url';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
import {defaultI18n, messageArguments, parseMessage} from '../../../packages/i18n/src/index.ts';
import {statesMessages} from '../../../packages/i18n/src/catalog/en-US/next/states.ts';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';
import {knownProgress, loadingPhase, skeletonGridWindow} from '../../src/kit/states/model.ts';
import {keyboardShortcutSections} from '../../src/kit/states/shortcuts.ts';

// Render the real DOM components. Styles are exercised in browser evidence;
// this loader supplies class names only for server-rendered structural tests.
const hooks = registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format:'module',shortCircuit:true,source:'export default new Proxy({}, {get: (_, key) => String(key)});'};
  if (url.endsWith('.tsx')) return {format:'module',shortCircuit:true,source:transformTsx(url)};
  return next(url,context);
}});
const {CountdownRing, DetailsPanel, StateView} = await import('../../src/kit/states/State.tsx');
const {GridSkeleton, HomeSkeleton, TitleSkeleton} = await import('../../src/kit/states/Skeletons.tsx');
const {StateSentence} = await import('../../src/kit/states/StateSentence.tsx');
test.after(() => hooks.deregister());

test('catalogue argument emphasis preserves the exact sentence and escapes supplied content', () => {
  const markup=renderToStaticMarkup(createElement(StateSentence,{messageId:'next.states.versionFailureBody',values:{failedVersion:'4K HDR10',alternative:'1080p',position:'1:12:40'}}));
  assert.equal(markup,"This browser couldn&#x27;t play the <strong>4K HDR10</strong> version. Your place is saved at 1:12:40, and the <strong>1080p</strong> version plays here as it is.");
  const escaped=renderToStaticMarkup(createElement(StateSentence,{messageId:'next.states.convertingOffBody',values:{title:'<script>private</script>'}}));
  assert.doesNotMatch(escaped,/<script>/);
  assert.match(escaped,/<strong>&lt;script&gt;private&lt;\/script&gt;<\/strong>/);
});

test('retry progress is supplied, bounded, and unknown never becomes a spinning or complete arc', () => {
  for (const value of [null, undefined, NaN, Infinity, -Infinity]) assert.equal(knownProgress(value),null);
  assert.equal(knownProgress(-.3),0);
  assert.equal(knownProgress(.4),.4);
  assert.equal(knownProgress(1.4),1);
  const unknown = renderToStaticMarkup(createElement(CountdownRing,{progress:null,accessibilityLabel:defaultI18n.t('next.states.retrying')}));
  assert.equal((unknown.match(/<circle/g)??[]).length,1);
  assert.doesNotMatch(unknown,/aria-valuenow|ringArc/);
  const known = renderToStaticMarkup(createElement(CountdownRing,{progress:.4,accessibilityLabel:defaultI18n.t('next.states.retrying')}));
  assert.match(known,/aria-valuenow="40"/);
  assert.match(known,/stroke-dashoffset="0.6"/);
});

test('loading thresholds are exactly 300 ms and eight seconds with no autonomous clock', () => {
  assert.equal(loadingPhase(299),'waiting');
  assert.equal(loadingPhase(300),'shapes');
  assert.equal(loadingPhase(7999),'shapes');
  assert.equal(loadingPhase(8000),'slow');
  assert.equal(loadingPhase(NaN),'waiting');
});

test('50,000 grid cells mount only the current window, including an incomplete last row', () => {
  const window = {width:1440,height:620,scrollTop:0};
  const first = skeletonGridWindow(window,50000);
  assert.equal(first.columns,7);
  assert.equal(first.cells.length,14);
  assert.ok(first.cells.every(cell => cell.index < 14));
  const distant = skeletonGridWindow({...window,scrollTop:first.rowHeight*5000},50000);
  assert.equal(distant.cells.length,14);
  assert.equal(distant.cells[0]!.index,35000);
  const tail = skeletonGridWindow({...window,scrollTop:first.rowHeight*7142},50000);
  assert.deepEqual(tail.cells.map(cell=>cell.index),[49994,49995,49996,49997,49998,49999]);
  assert.equal(skeletonGridWindow({...window,height:0},50000).cells.length,0);
  assert.equal(skeletonGridWindow({...window,scrollTop:first.totalHeight+1000},50000).cells.length,0);
});

test('phone grid preserves three poster columns and never stretches into desktop columns', () => {
  const geometry = skeletonGridWindow({width:390,height:600,scrollTop:0},50000);
  assert.equal(geometry.columns,3);
  assert.equal(geometry.cells[0]!.x,16);
  assert.ok(Math.abs(geometry.cellWidth-112.66666666666667)<.001);
  assert.ok(Math.abs(geometry.cells[0]!.height/geometry.cellWidth-1.5)<.001);
});

test('viewer Details omits owner codes and owner content even if an unsafe caller carries them', () => {
  const owner = {ownerMark:createElement('span',null,'supplied mark'),ownerTag:createElement('span',null,defaultI18n.t('next.states.ownerOnly')),paragraphs:['private-diagnostic'],codes:['decoder_contradiction'],actions:[]};
  const facts = [{id:'computer',label:defaultI18n.t('next.states.thisComputer'),value:defaultI18n.t('next.states.online')}];
  const viewer = renderToStaticMarkup(createElement(DetailsPanel,{audience:'viewer',facts,owner} as never));
  assert.match(viewer,/Online/);
  assert.doesNotMatch(viewer,/private-diagnostic|decoder_contradiction|Owner only|data-owner-diagnostics/);
  const authorised = renderToStaticMarkup(createElement(DetailsPanel,{audience:'owner',facts,owner}));
  assert.match(authorised,/private-diagnostic/);
  assert.match(authorised,/<code>decoder_contradiction<\/code>/);
  assert.match(authorised,/Owner only/);
});

test('error presentation preserves catalogue copy and cannot render a raw server message', () => {
  const presented = presentError({code:'request_failed',message:'private filesystem and bearer details'},'library');
  const markup = renderToStaticMarkup(createElement(StateView,{title:presented.title,body:presented.body}));
  assert.doesNotMatch(markup,/private filesystem|bearer|request_failed/);
  assert.ok(markup.includes(presented.body.replaceAll("'",'&#x27;')));
});

test('skeletons preserve arrived slots and pressed title while early loads draw no grey shapes', () => {
  const window = {width:1440,height:920,scrollTop:0};
  const early = renderToStaticMarkup(createElement(HomeSkeleton,{elapsedMs:299,window}));
  assert.doesNotMatch(early,/class="block/);
  const arrived = new Map([[0,createElement('button',{'data-arrived':true},'supplied item')],[500,createElement('button',{'data-outside':true},'offscreen item')]]);
  const grid = renderToStaticMarkup(createElement(GridSkeleton,{elapsedMs:500,window,itemCount:50000,arrived}));
  assert.match(grid,/data-arrived/);
  assert.doesNotMatch(grid,/data-outside|tabindex|tabIndex/);
  assert.equal((grid.match(/data-skeleton-index/g)??[]).length,skeletonGridWindow(window,50000).cells.length);
  const title = renderToStaticMarkup(createElement(TitleSkeleton,{elapsedMs:299,window,pressed:{title:'supplied pressed title'}}));
  assert.match(title,/supplied pressed title/);
  assert.doesNotMatch(title,/class="block/);
});

test('all drawn shortcuts exist and skip labels use actual Settings values', () => {
  const sections = keyboardShortcutSections(7,45);
  assert.deepEqual(sections.map(section=>section.id),['player','live','guide','anywhere']);
  assert.equal(sections[0]!.rows.length,18);
  assert.equal(sections[0]!.rows.find(row=>row.id==='back')!.label,'Back 7 seconds');
  assert.equal(sections[0]!.rows.find(row=>row.id==='forward')!.label,'Forward 45 seconds');
  assert.deepEqual(sections[1]!.rows.map(row=>row.id),['channel','guide']);
  assert.deepEqual(sections[2]!.rows.map(row=>row.id),['move','programme','watch','record','now']);
  for (const section of sections) for (const row of section.rows) for (const key of row.keys) assert.ok(defaultI18n.t(`next.states.key.${key}`));
});

test('every owned message parses and the shared retry key has the required numeric input', () => {
  for (const [key,value] of Object.entries(statesMessages)) assert.doesNotThrow(()=>parseMessage(value),key);
  assert.deepEqual([...messageArguments(statesMessages['next.states.retryIn'])],['seconds']);
  assert.equal(defaultI18n.t('next.states.retryIn',{seconds:1}),'Trying again in 1 second');
  assert.equal(defaultI18n.t('next.states.retryIn',{seconds:26}),'Trying again in 26 seconds');
  assert.doesNotMatch(statesMessages['next.states.ownerRequiredBody'],/Dana/);
});
