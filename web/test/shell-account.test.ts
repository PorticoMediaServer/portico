import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import type {ShellIdentity} from '../src/shell/Shell';
import type {AccountMenuProps} from '../src/kit/shell';

// Observe the real Shell -> AccountMenu boundary; portal rendering/comparison belongs to the kit.
const captured: AccountMenuProps[] = [];
const navigated: unknown[] = [];
Object.assign(globalThis, {__shellAccountCapture: captured, __shellAccountNavigate: navigated});
const router = `
  import {createElement} from ${JSON.stringify(import.meta.resolve('react'))};
  export const Link = ({children, to, activeProps, activeOptions, params, search, ...props}) => createElement('a', {...props, href: to}, children);
  export const useNavigate = () => value => { globalThis.__shellAccountNavigate.push(value); };
  export const useRouterState = ({select}) => select({location: {pathname: '/', search: {}}});
`;
const account = `
  import {createElement} from ${JSON.stringify(import.meta.resolve('react'))};
  export const AccountMenu = props => { globalThis.__shellAccountCapture.push(props); return props.trigger; };
  export const ProfileAvatarFace = ({initial}) => createElement('span', {}, initial);
  export const ProfileAvatar = ({initial, name, art, size, notificationDot, ...props}) => createElement('button', props, initial);
`;
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '@tanstack/react-router' && ['/src/shell/Shell.tsx', '/src/shell/DesktopNavigation.tsx'].some(path => context.parentURL?.endsWith(path))) return {url: 'shell-account:router', shortCircuit: true};
    if (specifier === '../kit/shell' && context.parentURL?.endsWith('/src/shell/Shell.tsx')) return {url: 'shell-account:account', shortCircuit: true};
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'shell-account:router') return {format: 'module', source: router, shortCircuit: true};
    if (url === 'shell-account:account') return {format: 'module', source: account, shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.svg')) return {format: 'module', source: 'export default "/wordmark.svg";', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  },
});
const {Shell} = await import('../src/shell/Shell.tsx');
function identity(username?: string, profileName = 'Evening profile'): ShellIdentity {
  return {username, profileName, serverName: 'Study server', profileArt: 'mint', primaryProfile: true,
    viewer: {serverId: 'server-a', accountId: 'account-a', profileId: 'profile-a', authority: 'local', role: 'owner'},
    profiles: [{id: 'profile-a', name: profileName, initial: 'E', art: 'mint'}, {id: 'profile-b', name: 'Kids', initial: 'K'}],
    capabilities:{manageAccount:true,managePeople:true,manageServer:true,manageSignIn:true},canManageProfiles: true, accountFacts: [{id: 'server', text: 'Study server', role: 'context'}],
    libraries: [], liveAvailable: false, unread: 2, showNotificationDot: true};
}
function render(value: ShellIdentity, signOut = () => {}, feedback?: () => void) {
  captured.length = 0;
  renderToStaticMarkup(createElement(Shell, {identity: value, OwnerActivity: () => null, dockVisible: false, onSignOut: signOut, onFeedback: feedback, children: null}));
  assert.equal(captured.length, 1);
  return captured[0];
}
test('Shell supplies genuine username and actual differing profile without dropping unrelated facts or switch targets', () => {
  const value = identity('account-user'), menu = render(value);
  assert.deepEqual(menu.current, {id: 'profile-a', username: 'account-user', name: 'Evening profile', initial: 'E', art: 'mint'});
  assert.equal(menu.accountFacts, value.accountFacts); assert.equal(menu.profiles, value.profiles);
  assert.equal(menu.primary, true);
});
test('a matching profile remains actual profile data for the kit comparison, with no duplicate identity fact', () => {
  const value = identity('same-name', 'same-name'), menu = render(value);
  assert.equal(menu.current.username, 'same-name'); assert.equal(menu.current.name, 'same-name');
  assert.equal(menu.current.initial, 's');
  assert.ok(menu.accountFacts.every(fact => fact.text !== 'same-name'));
});
test('missing genuine username stays absent even when account IDs and identity display facts are available', () => {
  const value = identity();
  const menu = render({...value, accountFacts: [...value.accountFacts, {id: 'account', text: 'Display label only', role: 'identity'}]});
  assert.equal(menu.current.username, undefined); assert.equal(menu.current.name, value.profileName);
  assert.equal(menu.accountFacts.length, 2);
});
test('a new account/profile authority replaces the displayed username and preserves genuine actions', () => {
  const first = render(identity('old-user'));
  let signedOut = 0;
  const nextIdentity = identity('new-user', 'New profile');
  const next = render({...nextIdentity, viewer: {...nextIdentity.viewer, accountId: 'new-account', profileId: 'new-profile'}}, () => { signedOut++; });
  assert.equal(first.current.username, 'old-user'); assert.equal(next.current.username, 'new-user');
  assert.equal(next.current.id, 'new-profile'); assert.equal(next.current.name, 'New profile');
  next.actions.signOut(); assert.equal(signedOut, 1);
  next.actions.switchProfile(nextIdentity.profiles[1]);
  assert.deepEqual(navigated.pop(), {to: '/profiles', search: {profileId: 'profile-b'}});
});

test('non-main profile cannot receive Manage or Add actions from the actual Shell boundary',()=>{
 const source=identity('owner');const menu=render({...source,primaryProfile:false,canManageProfiles:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false}});
 assert.equal(menu.actions.manageProfiles,undefined);assert.equal(menu.actions.addProfile,undefined);assert.equal(menu.serverAction,undefined);
 assert.equal(menu.signOutLabel,'Sign out of Study server');
});

test('feedback uses the supplied dialog action and never navigates off the current page', () => {
  assert.equal(render(identity('actual-user')).actions.feedback, undefined);
  let opened = 0;
  const menu = render(identity('actual-user'), () => {}, () => {opened++;});
  const before = navigated.length;menu.actions.feedback?.();
  assert.equal(opened, 1);assert.equal(navigated.length, before);
});
