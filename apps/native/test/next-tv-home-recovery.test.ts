import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';

/** states.html 5 on TV: the recovery's facts and actions belong to the ticket they were drawn for. */
test('Home recovery: actions carry their ticket, Details belongs to its ticket, and the facts name the address', async () => {
  const host = hooks();
  const {useTVHomeRecoveryView} = await screenModule(new URL('../src-next/pages/shared/home/useTVHomeRecoveryView.ts', import.meta.url), {
    react: host.react, '../../../../../../packages/i18n/src': {defaultI18n},
  });
  const first = {retired: false}, second = {retired: false}, retried: unknown[] = [], switched: unknown[] = [];
  const session = (ticket: object) => ({ticket, dueAt: undefined, delayMs: 0, attempts: 2, firstAt: Date.now(),
    presented: {title: 'Can’t reach Portico Demo', body: 'Body'}});
  const props = (ticket: object) => ({session: session(ticket) as any, onRetryNow: (value: unknown) => retried.push(value),
    onSwitchServer: (value: unknown) => switched.push(value), connectionFacts: {connectionAddress: 'https://demo.example'}});
  let view = host.render(() => useTVHomeRecoveryView(props(first)));
  assert.equal(view.title, 'Can’t reach Portico Demo');
  assert.ok(view.facts.some((line: string) => line.includes('https://demo.example')));
  view.retryNow(); view.switchServer!();
  assert.deepEqual(retried, [first]); assert.deepEqual(switched, [first]);
  view.openDetails();
  view = host.render(() => useTVHomeRecoveryView(props(first)));
  assert.equal(view.detailsOpen, true);
  view = host.render(() => useTVHomeRecoveryView(props(second)));
  assert.equal(view.detailsOpen, false, 'a later ticket never inherits the open panel');
  first.retired = true;
  view = host.render(() => useTVHomeRecoveryView(props(first)));
  view.openDetails();
  assert.equal(host.render(() => useTVHomeRecoveryView(props(first))).detailsOpen, false, 'a retired ticket opens nothing');
});
