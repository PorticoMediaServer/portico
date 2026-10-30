import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsSpec} from '../../../packages/client-core/src/presentation/settings-spec.ts';
import {tvServerCredentialCount, tvServerSettingsFacts} from '../src-next/screens/settings-live/server/tv-server-values.ts';

const section = (id: string) => settingsSpec.find(value => value.scope === 'server' && value.id === id)!;
const t = (id: string) => id, number = (value: number) => `number:${value}`;

test('TV values retain checked booleans/enums/numbers without exposing keys or arbitrary strings', () => {
  const read: string[] = [];
  const values: Record<string, unknown> = {'metadata.online': true, 'metadata.adult': false, 'metadata.language': 'en', 'metadata.region': 'injected-unknown-option', 'metadata.tmdb': 'private-token', 'metadata.osUser': 'private-username', 'metadata.osPass': 'private-password', 'metadata.acoustid': 'private-key'};
  const facts = tvServerSettingsFacts(section('metadata'), id => {read.push(id); return values[id];}, t, number);
  assert.deepEqual(facts.map(fact => fact.id), ['metadata.online', 'metadata.adult', 'metadata.language']);
  assert.equal(facts[0].value, 'next.settings.on');
  assert.equal(facts[1].value, 'next.settings.off');
  assert.ok(facts[2].value.startsWith('next.settingsSpec.'));
  assert.ok(!read.includes('metadata.tmdb') && !read.includes('metadata.osPass') && !read.includes('metadata.acoustid'));
  assert.ok(!JSON.stringify(facts).includes('private-'));
  const numeric = tvServerSettingsFacts(section('streaming'), id => id === 'streaming.uploadSpeed' ? 42 : id === 'streaming.awayExact' ? Infinity : undefined, t, number);
  assert.deepEqual(numeric.map(fact => [fact.id, fact.value]), [['streaming.uploadSpeed', 'number:42']]);
});

test('only the approved server name is admitted as free text; raw configuration/status strings stay out', () => {
  const facts = tvServerSettingsFacts(section('general'), id => id === 'general.name' ? 'Home server' : 'unreviewed-token-or-path', t, number);
  assert.deepEqual(facts.map(fact => [fact.id, fact.value]), [['general.name', 'Home server']]);
  for (const value of ['a'.repeat(121), 'line\nbreak', {token: 'private'}, undefined]) {
    assert.equal(tvServerSettingsFacts(section('general'), id => id === 'general.name' ? value : undefined, t, number).length, 0);
  }
});

test('integrations count is the exact server total from one page and refuses retirement or a bad total', async () => {
  const calls: string[] = [];
  assert.equal(await tvServerCredentialCount(async cursor => {calls.push(cursor); return {total: 1234};}, () => true), 1234);
  assert.deepEqual(calls, [''], 'one read; no client-side paging to count');
  await assert.rejects(tvServerCredentialCount(async () => ({total: -1}), () => true), /invalid_api_key_total/);
  await assert.rejects(tvServerCredentialCount(async () => ({total: 1.5}), () => true), /invalid_api_key_total/);
  let current = true;
  await assert.rejects(tvServerCredentialCount(async () => {current = false; return {total: 1};}, () => current), /native_tv_server_read_ended/);
});
