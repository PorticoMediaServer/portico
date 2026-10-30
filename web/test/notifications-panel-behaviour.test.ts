/** Offline checkpoint preparation: notification panel controls use supplied inbox facts. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactElement} from 'react';
import type {InboxSnapshot} from '@core/inbox.ts';
import {transformTsx} from './transform-tsx.mjs';
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {NotificationsPanel} = await import('../src/kit/shell/NotificationsPanel.tsx');
const {Button, Segmented} = await import('../src/kit/controls/index.ts');
function elements(node: unknown): ReactElement<any>[] {
  if (!isValidElement(node)) return [];
  const element = node as ReactElement<any>;
  return [element, ...Children.toArray(element.props.children).flatMap(elements)];
}
function panel(patch: Partial<InboxSnapshot> = {}) {
  const calls: {name: string; value?: unknown}[] = [];
  const snapshot: InboxSnapshot = {phase: 'ready', audience: 'profile', audiences: ['profile'], view: 'unread', items: [],
    counts: {unread: 3, read: 0, archived: 0, total: 3}, unread: 3, revision: 1, more: false, busy: false, ...patch};
  const nodes = elements(NotificationsPanel({snapshot, children: 'Real notification content',
    onAudience: value => calls.push({name: 'audience', value}), onView: value => calls.push({name: 'view', value}),
    onReadAll: () => calls.push({name: 'readAll'}), onMore: () => calls.push({name: 'more'})}));
  return {nodes, calls};
}

test('audience selector displays only available audiences and ignores unknown values', () => {
  const f = panel();
  const audience = f.nodes.filter(node => node.type === Segmented)[0];
  assert.deepEqual(audience.props.items.map((item: any) => item.value), ['profile']);
  audience.props.onValueChange('account-admin'); audience.props.onValueChange('unknown');
  assert.deepEqual(f.calls, []);
  audience.props.onValueChange('profile');
  assert.deepEqual(f.calls, [{name: 'audience', value: 'profile'}]);
});

test('view selector dispatches each supported view and rejects unknown values', () => {
  const f = panel(); const view = f.nodes.filter(node => node.type === Segmented)[1];
  for (const value of ['unread', 'all', 'archived', 'unknown']) view.props.onValueChange(value);
  assert.deepEqual(f.calls, ['unread', 'all', 'archived'].map(value => ({name: 'view', value})));
});

test('Mark all read is enabled only for ready inboxes with unread items', () => {
  for (const [phase, unread, disabled] of [['ready', 3, false], ['ready', 0, true], ['loading', 3, true], ['error', 3, true]] as const) {
    const f = panel({phase, counts: {unread, read: 0, archived: 0, total: unread}});
    const read = f.nodes.find(node => node.type === Button); assert.ok(read);
    assert.equal(read.props.disabled, disabled);
    if (!disabled) { read.props.onClick(); assert.deepEqual(f.calls, [{name: 'readAll'}]); }
  }
});

test('Load more appears only with a continuation and preserves busy feedback', () => {
  assert.equal(panel().nodes.filter(node => node.type === Button).length, 1);
  const f = panel({more: true, busy: true});
  const more = f.nodes.filter(node => node.type === Button)[1];
  assert.equal(more.props.loading, true);
  more.props.onClick(); assert.deepEqual(f.calls, [{name: 'more'}]);
});
