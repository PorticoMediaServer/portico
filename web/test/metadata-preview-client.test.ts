import assert from 'node:assert/strict';
import test from 'node:test';
import {bulkDraftValid, bulkFieldHint, bulkFieldStateLabel, bulkSummary} from '../src/kit/selection/presentation.ts';
import {screenLifetime, type ScreenContext} from '../src/screens/detail/context.ts';
import {readMetadataPreview} from '../../packages/client-core/src/home-selection/metadata-preview-preparation.ts';
import type {PreparationResponse} from '../../packages/client-core/src/read-preparation.ts';
import {componentModule, hooks} from './helpers/component-harness.mjs';
import * as presentation from '../src/kit/selection/presentation.ts';
import {selectionText} from '../src/kit/selection/copy.ts';

const many = {state: 'many' as const, atLeast: 65, truncated: true, values: [], entries: [{value: 'Kept', count: 17}]};
const spec = {field: 'studio', label: 'Studio', group: 'general' as const, type: 'text' as const, bulk: true};
const draft = {fields: {}, lists: {}, lockEdited: true};
const tick = async () => {for (let i = 0; i < 25; i++) await Promise.resolve();};

test('many copy and summary never invent an exact count or write an untouched field', () => {
  assert.equal(bulkFieldStateLabel(many), 'Many values');
  assert.equal(bulkFieldHint(many, 70, false, false), undefined);
  assert.equal(bulkFieldHint(many, 70, true, false), 'was Mixed');
  assert.equal(bulkDraftValid([spec], {studio: many}, draft), false);
  assert.deepEqual(bulkSummary([spec], {studio: many}, draft, 70), []);
  const changed = {...draft, fields: {studio: 'Kept'}};
  assert.equal(bulkDraftValid([spec], {studio: many}, changed), true);
  const summary = bulkSummary([spec], {studio: many}, changed, 70);
  assert.equal(summary[0]!.changedCount, undefined); assert.equal(summary[0]!.changedTitles, undefined);
  const exact = {...many, state: 'mixed' as const, atLeast: undefined, distinctCount: 2, truncated: false, values: [{value: 'Kept', count: 17}, {value: '', count: 53}]};
  assert.equal(bulkFieldHint(exact, 70, false, false), '2 values');
  assert.equal(bulkSummary([spec], {studio: exact}, changed, 70)[0]!.changedCount, 53);
});

test('actual web scalar, enum and list field branches show Many values without a numeric lower bound', async () => {
  const h = hooks(); let active = h;
  const react = {...h.react, useState: (...args: any[]) => (active.react.useState as any)(...args), useId: () => 'field',
    createElement: (type: any, props: any, ...children: any[]) => ({type, props: {...props, children: children.length ? children : props?.children}})};
  const {BulkEditor} = await componentModule(new URL('../src/kit/selection/BulkEditor.tsx', import.meta.url), {
    react: {...react, default: react}, '@i18n': {defaultI18n: {number: String, list: (values: string[]) => values.join(', ')}},
    '../controls': {Button: 'Button', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, '../overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog'},
    '../overlays/shared': {usePhone: () => false}, '../Icon': {Icon: 'Icon'}, './copy': {selectionText}, './presentation': presentation,
    './selection.module.css': {default: new Proxy({}, {get: (_, key) => String(key)})},
  });
  const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node && typeof node === 'object' && node.props ? [node, ...nodes(node.props.children)] : [];
  const content = (node: any): string => Array.isArray(node) ? node.map(content).join(' ') : node && typeof node === 'object' && node.props ? content(node.props.children) : typeof node === 'string' || typeof node === 'number' ? String(node) : '';
  for (const field of [spec, {...spec, type: 'enum', allowed: ['A']}, {...spec, field: 'tags', type: 'list'}]) {
    const changes: unknown[] = [];
    active = hooks();
    const tree = active.render(() => BulkEditor({open: true, onOpenChange() {}, selection: {kind: 'movie', count: 70}, schema: [field], singleItemFields: [], values: {[field.field]: many}, draft, onDraftChange: (next: unknown) => changes.push(next), onSave() {}, onRetry() {}, targets: []}));
    const child = nodes(tree).find(node => node.props.spec?.field === field.field)!;
    assert.ok(child, JSON.stringify(tree, (_, value) => typeof value === 'function' ? value.name : value));
    active = hooks();
    const rendered = active.render(() => child.type(child.props));
    const label = field.type === 'enum' ? nodes(rendered).find(node => node.type === 'Select')!.props.placeholder : content(rendered);
    assert.match(label, /Many values/); assert.doesNotMatch(label, /65|70 values/); assert.deepEqual(changes, []);
    if (field.type === 'text') {
      const input = nodes(rendered).find(node => node.type === 'TextField')!;
      assert.equal(input.props.value, ''); input.props.onChange({currentTarget: {value: 'New'}});
      assert.deepEqual(changes, [{...draft, fields: {studio: 'New'}}]);
    }
    if (field.type === 'list') assert.match(content(rendered), /Kept/);
  }
});

test('real screen raw wrapper permits late metadata handle cleanup after retirement', async () => {
  let finish!: (response: PreparationResponse) => void;
  const calls: string[] = [], path = '/v1/metadata/bulk/preview', poll = `${path}-queries/private-1`;
  const api = {request: async () => {throw Error('body-only transport');}, requestRaw: async (target: string, method: string, options: any) => {
    calls.push(`${method} ${target}`);
    if (method === 'POST') return new Promise<PreparationResponse>(resolve => {finish = resolve;});
    assert.equal(options.singleAttempt, true); assert.equal(options.signal, undefined); return {status: 204, headers: {}};
  }};
  const life = screenLifetime({api, isCurrent: () => true} as unknown as ScreenContext);
  const work = readMetadataPreview(life.api, {targets: [{kind: 'item', contentKind: 'movie', id: 'one', libraryId: 'lib'}]}, () => {throw Error('late publish');}, {signal: life.signal(), isCurrent: life.current});
  await tick(); life.dispose(); await assert.rejects(work, {name: 'AbortError'});
  finish({status: 202, headers: {Location: poll}, body: {variant: 'preparing', kind: 'metadata-preview-preparing', state: 'preparing', queryId: 'private-1', retryAfterMs: 1000}}); await tick();
  assert.deepEqual(calls, [`POST ${path}`, `DELETE ${poll}`]);
  await assert.rejects(life.api.requestRaw('/v1/unrelated', 'GET'), {name: 'AbortError'});
});
