import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {SongRow, TrackTable} = await import('../../src/kit/browse/TrackTable.tsx');
const {FacetChips, MatchMode} = await import('../../src/kit/browse/FilterBuilder.tsx');
const row = {id: 'actual-song', number: 1, text: {id: 'actual-song', title: 'Actual song', caption: [], detail: []}};
test('a search selection control is independent of play, even without artwork', () => {
  for (const artwork of [undefined, createElement('img', {alt: '', src: '/actual-image'})]) {
    const html = renderToStaticMarkup(createElement(SongRow, {row: {...row, artwork}, context: 'search', selection: createElement('button', {'aria-label': 'Select actual song'}, 'Select'), onPlay() {}}));
    assert.match(html, /songSelection/);
    assert.doesNotMatch(html, /<button[^>]*>(?:(?!<\/button>).)*<button/s);
    assert.equal((html.match(/<button/g) ?? []).length, 2);
  }
});
test('exclusion has a distinct accessible state and known zero facet counts remain visible', () => {
  const html = renderToStaticMarkup(createElement(FacetChips, {values: [{value: 'a', label: 'Actual genre', count: 0}], included: [], excluded: ['a'], onValueChange() {}}));
  assert.match(html, /role="checkbox" aria-checked="mixed"/);
  assert.match(html, /<small>0<\/small>/);
  assert.match(html, /aria-label="Clear Actual genre"/);
});
test('unsupported match modes are omitted and sparse facts stay absent', () => {
  const match = renderToStaticMarkup(createElement(MatchMode, {value: 'any', allowAll: false, allowNone: false, onValueChange() {}}));
  assert.doesNotMatch(match, /All of these|None of these/);
  const html = renderToStaticMarkup(createElement(TrackTable, {rows: [row], variant: 'album', label: 'Actual album', onPlay() {}, onMore() {}}));
  assert.doesNotMatch(html, /Unknown|1:00|Invalid Date/);
  assert.match(html, /aria-rowcount="2"/);
  assert.throws(() => renderToStaticMarkup(createElement(TrackTable, {rows: [row], variant: 'songs', label: 'Actual songs', onPlay() {}, onMore() {}})), /Invalid controlled track window/);
});

test('range presets are exclusive controlled toggles rather than independent checkboxes', async () => {
  const {FilterBuilder, FilterFieldBody} = await import('../../src/kit/browse/FilterBuilder.tsx');
  const field = {id: 'durationMs', labelKey: 'browse.field.durationSeconds', type: 'number', operators: ['at-most'], controlHint: 'number-range', complexity: 'standard', cost: 'indexed-join', applicableKinds: ['movie']};
  const state = {kind: 'range', value: 'short', summary: 'Under 30 min', options: [{value: 'short', label: 'Under 30 min'}]};
  const handlers = {onFieldOpenChange() {}, onFieldChange() {}, onSearchChange() {}};
  // H10: on desktop the open field's options are drawn in a popover anchored to its tile; this is that body.
  const html = renderToStaticMarkup(createElement(FilterFieldBody, {field, state, label: 'Duration', contentId: 'duration', ...handlers} as never));
  assert.match(html, /aria-pressed="true"[^>]*>Under 30 min/);
  assert.doesNotMatch(html, /role="checkbox"/);
  // The grid keeps the open field as its tile (expanded, still summarised) instead of expanding inline.
  const grid = renderToStaticMarkup(createElement(FilterBuilder, {model: {groups: [{id: 'file', fields: [field]}]}, labels: {field: () => 'Duration'}, fields: {durationMs: state}, openFields: ['durationMs'], ...handlers} as never));
  assert.match(grid, /aria-expanded="true"[^>]*>(?:(?!<\/button>).)*Under 30 min/s);
  assert.doesNotMatch(grid, /aria-pressed/);
});

test('retained facet chips and selected states remain beside refresh and error notices', async () => {
  const {FilterBuilder, FilterFieldBody} = await import('../../src/kit/browse/FilterBuilder.tsx');
  for (const platform of ['desktop','phone','tv'] as const) for (const status of ['loading','error'] as const) {
    const field={id:'genre',labelKey:'filter.field.genre',type:'identity-set',operators:['equals','not-in'],controlHint:'facet-multi-select',complexity:'quick',cost:'indexed',applicableKinds:['movie']};
    const state={kind:'values',match:'any',query:'',status,error:'Refresh failed',included:['retained'],excluded:['excluded'],values:[{value:'retained',label:'Retained choice',count:4},{value:'excluded',label:'Excluded choice',count:2}]};
    const handlers={onFieldOpenChange(){},onFieldChange(){},onSearchChange(){},onRetry(){}};
    // Desktop draws the open field in an anchored popover (H10); phone and TV draw it inline in the builder.
    const html=platform==='desktop'
      ?renderToStaticMarkup(createElement(FilterFieldBody,{field,state,label:'Genre',platform,contentId:'genre',...handlers} as never))
      :renderToStaticMarkup(createElement(FilterBuilder,{platform,model:{groups:[{id:'title',fields:[field]}]},labels:{field:()=> 'Genre'},fields:{genre:state},openFields:['genre'],...handlers} as never));
    assert.match(html,/role="checkbox" aria-checked="true"/);
    assert.match(html,/role="checkbox" aria-checked="mixed"/);
    assert.match(html,/Retained choice/);assert.match(html,/Excluded choice/);
    assert.match(html,status==='loading'?/role="status"/:/role="alert"/);
    if(status==='error'){assert.match(html,/Refresh failed/);assert.match(html,/Try again/);}
    assert.doesNotMatch(html,/No values/);
  }
});
