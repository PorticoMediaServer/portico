import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createPeopleDevicesApi} from '../src/screens/settings/people-devices-api.ts';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('on a phone People is owner-people 24a\'s tabs: Accounts, Invitations and Devices; wider screens keep the stacked groups', () => {
  const people = read('../src/screens/settings/People.tsx');
  assert.match(people, /\{phone && <Tabs phone className=\{peopleStyles\.phoneTabs\} value=\{peopleTab\}/);
  assert.match(people, /\{value: 'accounts', label: t\('settings\.server\.accounts'\)\}/);
  assert.match(people, /label: counted\(t\('settings\.server\.invitations'\), tabCounts\.invitations\)/);
  assert.match(people, /label: counted\(t\('settings\.section\.devices'\), tabCounts\.devices\)/);
  // Panes stay mounted (counts known before opening) and leave the flow when not chosen; desktop never hides one.
  assert.match(people, /data-hidden=\{phone && peopleTab !== 'accounts' \|\| undefined\}/);
  assert.match(people, /<PeopleDevicesAll api=\{bindings\.devices\} isCurrent=\{bindings\.authority\.isCurrent\} canSignOut=\{bindings\.actor\.role === 'owner'\}/);
  const css = read('../src/screens/settings/people-tabs.module.css');
  assert.match(css, /\.pane \{ display: contents; \}/);
  assert.match(css, /\.pane\[data-hidden\] \{ display: none; \}/);
  assert.match(read('../src/screens/settings/PeopleInvitations.tsx'), /useEffect\(\(\) => \{onTotal\?\.\(total\);\}, \[onTotal, total\]\);/);
});

test('the Devices tab lists every device: an empty account drops the filter; a person\'s list keeps it', async () => {
  const paths: string[] = [];
  const api = createPeopleDevicesApi({request: async <T>(path: string) => {paths.push(path); return {limit: 100, nextCursor: '', items: []} as T;}}, () => true);
  await api.list('', '', new AbortController().signal);
  await api.list('acct_1', '', new AbortController().signal);
  assert.equal(paths[0]!.includes('account='), false);
  assert.match(paths[1]!, /&account=acct_1$/);
});
