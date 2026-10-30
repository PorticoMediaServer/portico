import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, createElement, isValidElement, type ReactElement, type ReactNode} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import type {ShellIdentity} from '../src/shell/Shell';
import type {AccountMenuProps} from '../src/kit/shell';
import {transformTsx} from './transform-tsx.mjs';

const state = {width: 390, pathname: '/', navigated: [] as unknown[], menus: [] as AccountMenuProps[]};
Object.assign(globalThis, {__mobileActivityTest: state});
const actualAccount = new URL('../src/kit/shell/AccountSurfaces.tsx', import.meta.url).href;
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '@tanstack/react-router' && context.parentURL?.endsWith('/src/shell/DesktopNavigation.tsx')) return {url: 'mobile-activity:router', shortCircuit: true};
    if (context.parentURL?.endsWith('/src/shell/Shell.tsx')) {
      if (specifier === './viewport') return {url: 'mobile-activity:viewport', shortCircuit: true};
      if (specifier === '../kit/theme') return {url: 'mobile-activity:theme', shortCircuit: true};
      if (specifier === '@tanstack/react-router') return {url: 'mobile-activity:router', shortCircuit: true};
      if (specifier === '../kit/shell') return {url: 'mobile-activity:account', shortCircuit: true};
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'mobile-activity:viewport') return {format: 'module', source: 'export const useShellWidth = () => globalThis.__mobileActivityTest.width; export const useShellScrolled = () => false;', shortCircuit: true};
    if (url === 'mobile-activity:theme') return {format: 'module', source: "export const useSizeClass=()=>globalThis.__mobileActivityTest.width<600?'compact':globalThis.__mobileActivityTest.width<1024?'medium':'expanded'; export const useCoarsePointer=()=>false; export const useWindowScrollAnchor=()=>{};", shortCircuit: true};
    if (url === 'mobile-activity:router') return {format: 'module', source: `
      import {createElement} from ${JSON.stringify(import.meta.resolve('react'))};
      export const Link = ({children, to, activeProps, activeOptions, params, search, ...props}) => createElement('a', {...props, href: to}, children);
      export const useNavigate = () => value => { globalThis.__mobileActivityTest.navigated.push(value); };
      export const useRouterState = ({select}) => select({location: {pathname: globalThis.__mobileActivityTest.pathname, search: {}}});
    `, shortCircuit: true};
    if (url === 'mobile-activity:account') return {format: 'module', source: `
      import {createElement} from ${JSON.stringify(import.meta.resolve('react'))};
      import {AccountMenu as ActualAccountMenu} from ${JSON.stringify(actualAccount)};
      export {ProfileAvatar, ProfileAvatarFace} from ${JSON.stringify(actualAccount)};
      export const AccountMenu = props => { globalThis.__mobileActivityTest.menus.push(props); return createElement(ActualAccountMenu, props); };
    `, shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.svg')) return {format: 'module', source: 'export default "/wordmark.svg";', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  },
});
const {Shell} = await import('../src/shell/Shell.tsx');
const {AccountMenu} = await import('../src/kit/shell/AccountSurfaces.tsx');
const {MenuSeparator} = await import('../src/kit/overlays/Popover.tsx');
function identity(role: string): ShellIdentity {
  return {capabilities:{manageAccount:true,manageServer:['owner','admin','administrator'].includes(role),managePeople:['owner','admin','administrator'].includes(role),manageSignIn:role==='owner'},viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role},
    username: 'actual-account', profileName: 'Actual profile', serverName: 'Actual server', primaryProfile: true,
    profiles: [], canManageProfiles: false, accountFacts: [], libraries: [], liveAvailable: false, unread: 0, showNotificationDot: false};
}
function render(role: string, width = 390, pathname = '/') {
  Object.assign(state, {width, pathname, menus: [], navigated: []});
  let mounts = 0;
  const html = renderToStaticMarkup(createElement(Shell, {identity: identity(role), OwnerActivity: () => { mounts++; return createElement('span', {}, 'Owner-only reads'); }, dockVisible: false, onSignOut() {}, children: null}));
  return {html, mounts, menu: state.menus[0]};
}
function elements(node: ReactNode): ReactElement<{children?: ReactNode}>[] {
  return Children.toArray(node).filter(isValidElement) as ReactElement<{children?: ReactNode}>[];
}
function find(node: ReactNode, predicate: (element: ReactElement<Record<string, unknown>>) => boolean): ReactElement<Record<string, unknown>> | undefined {
  for (const element of elements(node)) {
    if (predicate(element as ReactElement<Record<string, unknown>>)) return element as ReactElement<Record<string, unknown>>;
    const nested = find(element.props.children, predicate); if (nested) return nested;
  }
}

test('compact owner Home keeps the drawn wordmark and avatar without an Activity glyph or private reads', () => {
  for (const width of [390, 599]) {
    const {html, mounts} = render('owner', width);
    assert.equal(mounts, 0);
    const header = html.match(/<header class="phoneHeader"[^>]*>.*?<\/header>/s)?.[0];
    assert.ok(header);
    assert.match(header, /class="brand".*<img[^>]*src="\/wordmark.svg".*class="phoneTools".*aria-label="Actual profile, account menu"/s);
    assert.doesNotMatch(html, /phoneActivity|i-activity|aria-label="Server activity"|data-nav-activity/);
    assert.doesNotMatch(html, /Owner-only reads|data-busy|streamCount|updateDot|Running normally|No streams/);
    assert.match(html, />Libraries<\/span>/);
  }
});
test('medium owner rail mounts only its genuine owner Activity source and keeps the profile pinned', () => {
  for (const width of [600, 844, 1023]) {
    const {html, mounts} = render('owner', width);
    assert.equal(mounts, 1);
    assert.match(html, /data-navigation="rail"/);
    assert.match(html, /class="railBottom".*Owner-only reads.*Settings.*Actual profile, account menu/s);
    assert.doesNotMatch(html, /class="phoneHeader"/);
  }
});
test('administrator, member and unknown roles never get the owner Home glyph or its private component', () => {
  for (const role of ['admin', 'administrator', 'member', 'unknown', '']) for (const width of [390, 600, 1023, 1440]) {
    const {html, mounts} = render(role, width);
    assert.equal(mounts, 0); assert.doesNotMatch(html, /i-activity|aria-label="Server activity"|data-nav-activity|Owner-only reads/);
  }
});
test('genuine owner and administrator account actions navigate to their existing admitted Dashboard destination', () => {
  for (const role of ['owner', 'admin', 'administrator']) {
    const {menu} = render(role);
    assert.ok(isValidElement<{onSelect: () => void}>(menu.serverAction));
    menu.serverAction.props.onSelect();
    assert.deepEqual(state.navigated, [{to: '/dashboard', search: role === 'owner' ? {} : {section: 'streams'}}]);
  }
  for (const role of ['member', 'unknown', '']) assert.equal(render(role).menu.serverAction, undefined);
});
test('real AccountMenu places its optional server action immediately after profile switches and before the first separator', () => {
  const {menu} = render('owner');
  const content = elements(AccountMenu(menu).props.children)[1];
  const rows = Children.toArray(content.props.children);
  const slot = rows.findIndex(row => isValidElement<{onSelect?: unknown}>(row) && isValidElement<{onSelect?: unknown}>(menu.serverAction) && row.props.onSelect === menu.serverAction.props.onSelect);
  assert.ok(slot > 0);
  assert.ok(isValidElement<{className?: string}>(rows[slot - 1]));
  assert.equal((rows[slot - 1] as ReactElement<{className?: string}>).props.className, 'switchProfiles');
  assert.ok(isValidElement(rows[slot + 1]));
  assert.equal((rows[slot + 1] as ReactElement).type, MenuSeparator);
  const noSlot = {...menu, serverAction: undefined};
  assert.equal(find(AccountMenu(noSlot), element => element === menu.serverAction), undefined);
});
test('compact Home keeps its account action and tab navigation without a private Activity control', () => {
  const {html, mounts} = render('owner');
  const header = html.match(/<header class="phoneHeader"[^>]*>.*?<\/header>/s)?.[0];
  assert.ok(header);assert.equal(mounts, 0);
  assert.equal((header.match(/<button\b/g) ?? []).length, 1);
  assert.match(header, /aria-label="Actual profile, account menu"/);
  assert.match(html, /<nav[^>]*class="tabbar"[^>]*>.*href="\/search".*>Libraries<\/span>.*href="\/saved"/s);
  assert.doesNotMatch(html, /phoneActivity|i-activity|aria-label="Server activity"|data-nav-activity|Owner-only reads/);
});
test('desktop retains only its existing real owner Activity component and non-Home mobile routes add no header glyph', () => {
  assert.equal(render('owner', 1024).mounts, 1);
  for (const role of ['admin', 'administrator', 'member']) assert.equal(render(role, 1440).mounts, 0);
  for (const path of ['/search', '/libraries', '/dashboard']) {
    const {html, mounts} = render('owner', 390, path);
    assert.equal(mounts, 0); assert.doesNotMatch(html, /phoneHeader|phoneActivity|i-activity/);
  }
});
test('E-web-21: the switcher names the main profile as the Main profile to assistive tech, and only that profile', () => {
  const {menu} = render('owner');
  const profiles = [{id: 'main', name: 'walkowner', initial: 'W', primary: true}, {id: 'guest', name: 'Guest', initial: 'G'}, {id: 'kids', name: 'Kids', initial: 'K'}];
  const tree = AccountMenu({...menu, current: profiles[1], primary: false, profiles});
  const labelled = (name: string) => find(tree, element => String(element.key).endsWith('$' + name));
  assert.equal(labelled('main')?.props['aria-label'], 'walkowner, Main profile');
  assert.equal(labelled('kids')?.props['aria-label'], undefined, 'other profiles keep their visible name as their label');
});
