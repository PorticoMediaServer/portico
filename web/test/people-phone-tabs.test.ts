import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync, existsSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('on a phone People is owner-people 24a\'s tabs, Accounts and Invitations, without counts; wider screens keep the stacked groups', () => {
  const people = read('../src/screens/settings/People.tsx');
  assert.match(people, /\{phone && <Tabs phone className=\{peopleStyles\.phoneTabs\} value=\{peopleTab\}/);
  assert.match(people, /items=\{\[\{value: 'accounts', label: t\('settings\.server\.accounts'\)\},\n\s+\.\.\.\(bindings\.invitations \? \[\{value: 'invitations', label: t\('settings\.server\.invitations'\)\}\] : \[\]\)\]\}/);
  // Superseded 22 Oct: no Devices tab (a person's devices are on their own page), and no counts on the tabs.
  assert.doesNotMatch(people, /value: 'devices'|PeopleDevicesAll|tabCount|counted\(/);
  assert.equal(existsSync(new URL('../src/screens/settings/PeopleDevicesAll.tsx', import.meta.url)), false);
  assert.match(people, /data-hidden=\{phone && peopleTab !== 'accounts' \|\| undefined\}/);
  const css = read('../src/screens/settings/people-tabs.module.css');
  assert.match(css, /\.pane \{ display: contents; \}/);
  assert.match(css, /\.pane\[data-hidden\] \{ display: none; \}/);
  assert.doesNotMatch(css, /tabCount/);
});
