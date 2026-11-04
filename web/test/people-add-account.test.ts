import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('Add account is offered only to the owner of a local-sign-in server (server-people "Add account"; the server refuses anyone else)', () => {
  const route = read('../src/screens/settings/Settings.route.tsx');
  assert.match(route, /\.\.\.\(role === 'owner' && identity\.signIn === 'local' \? \{addAccount: \(input: NewDirectMember, signal: AbortSignal\) => \{assert\(\); return createDirectMember\(api, input, signal\);\}\} : \{\}\),/);
});

test('the People heading draws Add account (glass) before Invite someone, as owner-people 24a and the spec draw the pair', () => {
  const people = read('../src/screens/settings/People.tsx');
  assert.match(people, /actions=\{<>\{bindings\.addAccount && <Button size="small" variant="glass"[\s\S]*?\{t\('web\.people\.addAccount'\)\}<\/Button>\}<Button size="small" variant="primary"/);
  assert.match(people, /onAdded=\{\(\) => \{if \(mounted\.current && bindings\.authority\.isCurrent\(\)\) setRefresh\(value => value \+ 1\);\}\}/);
});

test('the dialog follows owner-people 6: Username, Profile name, Password with the strength meter, Libraries; ready only when complete and strong', () => {
  const dialog = read('../src/screens/settings/people-add-account.tsx');
  const order = ['accounts.signIn.username', 'web.people.profileName', 'accounts.signIn.password', 'web.people.visibleLibraries'].map(key => dialog.indexOf(`t('${key}')`));
  assert.ok(order.every((at, index) => at > 0 && (index === 0 || at > order[index - 1]!)), JSON.stringify(order));
  assert.match(dialog, /<ServicesStrength password=\{password\} id=\{strength\} \/>/);
  assert.match(dialog, /const ready = directMemberInputReady\(input\) && passwordStrength\(password\)\.acceptable && \(all \|\| chosen\.length > 0\);/);
  assert.match(dialog, /error=\{failure === 'taken' \? t\('web\.directJoin\.error\.taken'\) : undefined\}/);
});
