import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n, messageArguments, parseMessage} from '../../../packages/i18n/src/index.ts';
import {statesMessages} from '../../../packages/i18n/src/catalog/en-US/next/states.ts';
import {knownProgress, loadingPhase, skeletonGridWindow} from '../../src/kit/states/model.ts';
import {keyboardShortcutSections} from '../../src/kit/states/shortcuts.ts';

test('unknown retry work stays unknown and measured progress remains bounded', () => {
  for (const value of [null,undefined,NaN,Infinity,-Infinity]) assert.equal(knownProgress(value),null);
  assert.equal(knownProgress(-.3),0);
  assert.equal(knownProgress(.4),.4);
  assert.equal(knownProgress(1.4),1);
});
test('loading thresholds are exactly 300 ms and eight seconds', () => {
  assert.equal(loadingPhase(299),'waiting'); assert.equal(loadingPhase(300),'shapes');
  assert.equal(loadingPhase(7999),'shapes'); assert.equal(loadingPhase(8000),'slow');
  assert.equal(loadingPhase(NaN),'waiting');
});
test('a 50,000 item library mounts only the current rows and its incomplete last row', () => {
  const window={width:1440,height:620,scrollTop:0};
  const first=skeletonGridWindow(window,50000);
  assert.equal(first.columns,7); assert.equal(first.cells.length,14);
  const distant=skeletonGridWindow({...window,scrollTop:first.rowHeight*5000},50000);
  assert.equal(distant.cells.length,14); assert.equal(distant.cells[0]!.index,35000);
  const last=skeletonGridWindow({...window,scrollTop:first.rowHeight*7142},50000);
  assert.deepEqual(last.cells.map(cell=>cell.index),[49994,49995,49996,49997,49998,49999]);
  assert.equal(skeletonGridWindow({...window,height:0},50000).cells.length,0);
  assert.equal(skeletonGridWindow({...window,scrollTop:first.totalHeight+1000},50000).cells.length,0);
});
test('phone loading matches the drawn gutters, gaps, three columns and poster ratio', () => {
  const geometry=skeletonGridWindow({width:390,height:600,scrollTop:0},50000);
  assert.equal(geometry.columns,3); assert.equal(geometry.cells[0]!.x,16);
  assert.ok(Math.abs(geometry.cellWidth-112.66666666666667)<.001);
  assert.ok(Math.abs(geometry.cells[0]!.height/geometry.cellWidth-1.5)<.001);
  assert.equal(geometry.rowHeight,geometry.cells[0]!.height+41+16);
});
test('known caption extents preserve the drawn leading rows without mounting the library', () => {
  const window={width:390,height:600,scrollTop:0};
  const options={captions:false,leadingCaptionLines:[2,1] as const};
  const geometry=skeletonGridWindow(window,50000,options);
  assert.equal(geometry.cells[3]!.y,geometry.cells[0]!.height+41+16);
  assert.equal(geometry.cells[6]!.y,geometry.cells[3]!.y+geometry.cells[0]!.height+20+16);
  const distant=skeletonGridWindow({...window,scrollTop:1000000},50000,options);
  assert.ok(distant.cells.length<=15);
  assert.ok(distant.cells[0]!.index>10000);
  assert.equal(skeletonGridWindow({...window,scrollTop:geometry.totalHeight+1},50000,options).cells.length,0);
});
test('empty and nonfinite inputs cannot turn into unbounded work or invalid geometry', () => {
  const window={width:1440,height:620,scrollTop:0};
  assert.equal(skeletonGridWindow(window,NaN).cells.length,0);
  assert.equal(skeletonGridWindow({...window,width:0},50000).cells.length,0);
  assert.deepEqual(skeletonGridWindow(window,50000,{columns:NaN,gutter:Infinity,columnGap:NaN,rowGap:NaN}),skeletonGridWindow(window,50000));
});
test('oversized finite library counts still produce a bounded visible window', () => {
  const geometry=skeletonGridWindow({width:1440,height:620,scrollTop:1000000},Number.MAX_VALUE);
  assert.ok(Number.isFinite(geometry.totalHeight));
  assert.ok(geometry.cells.length>0 && geometry.cells.length<=21);
  assert.ok(geometry.cells.every(cell=>Number.isSafeInteger(cell.index)));
});
test('the complete shortcut list uses actual Settings skip values', () => {
  const sections=keyboardShortcutSections(7,45);
  assert.deepEqual(sections.map(section=>section.id),['player','live','guide','anywhere']);
  assert.equal(sections[0]!.rows.length,18);
  assert.equal(sections[0]!.rows.find(row=>row.id==='back')!.label,'Back 7 seconds');
  assert.equal(sections[0]!.rows.find(row=>row.id==='forward')!.label,'Forward 45 seconds');
  assert.deepEqual(sections[1]!.rows.map(row=>row.id),['channel','guide']);
  assert.deepEqual(sections[2]!.rows.map(row=>row.id),['move','programme','watch','record','now']);
  for(const section of sections) for(const row of section.rows) for(const key of row.keys) assert.ok(defaultI18n.t(`next.states.key.${key}`));
});
test('catalogue copy parses and retry pluralization remains real', () => {
  for(const [key,value] of Object.entries(statesMessages)) assert.doesNotThrow(()=>parseMessage(value),key);
  assert.deepEqual(messageArguments(statesMessages['next.states.retryIn']),['seconds']);
  assert.equal(defaultI18n.t('next.states.retryIn',{seconds:1}),'Trying again in 1 second');
  assert.equal(defaultI18n.t('next.states.retryIn',{seconds:26}),'Trying again in 26 seconds');
  assert.doesNotMatch(statesMessages['next.states.ownerRequiredBody'],/Dana/);
});
