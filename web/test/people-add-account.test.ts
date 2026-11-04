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
  const form = dialog.slice(dialog.indexOf('<form'));
  const order = [`t('accounts.signIn.username')`, `t('web.people.profileName')`, `t('accounts.signIn.password')`, '<AddAccountLibraries'].map(mark => form.indexOf(mark));
  assert.ok(order.every((at, index) => at > 0 && (index === 0 || at > order[index - 1]!)), JSON.stringify(order));
  assert.match(dialog, /<ServicesStrength password=\{password\} id=\{strength\} \/>/);
  assert.match(dialog, /const ready = directMemberInputReady\(input\) && passwordStrength\(password\)\.acceptable && \(all \|\| chosen\.length > 0\);/);
  assert.match(dialog, /error=\{failure === 'taken' \? t\('web\.directJoin\.error\.taken'\) : undefined\}/);
});

test('on a phone the libraries are frame 25\'s grouped row, "Libraries they can see" with its value, not the desktop checkboxes', async () => {
  const {registerHooks} = await import('node:module');
  const {transformTsx} = await import('./transform-tsx.mjs');
  registerHooks({load(url, context, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  const {createElement} = await import('react');
  const {renderToStaticMarkup} = await import('react-dom/server');
  const {AddAccountLibraries} = await import('../src/screens/settings/people-add-account.tsx');
  const libraries = [{id: 'a', name: 'Movies'}, {id: 'b', name: 'Music'}];
  const props = {libraries, all: true, chosen: [], disabled: false, onAll() {}, onChosen() {}, onPick() {}};
  const phone = renderToStaticMarkup(createElement(AddAccountLibraries, {...props, phone: true}));
  assert.match(phone, /<button type="button"[^>]*>.*Libraries they can see.*All libraries.*<\/button>/s);
  assert.doesNotMatch(phone, /type="checkbox"/);
  assert.match(renderToStaticMarkup(createElement(AddAccountLibraries, {...props, phone: true, all: false, chosen: ['a']})), /1 library/);
  const desktop = renderToStaticMarkup(createElement(AddAccountLibraries, {...props, phone: false}));
  assert.match(desktop, /type="checkbox"/);
  assert.match(desktop, /All libraries, including ones added later/);
});
