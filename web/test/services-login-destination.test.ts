import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';
import {captureHostedOwnerClaim} from '../src/services/owner-claim.ts';
import {providerReturnPath, restoreHostedOwnerPending, type HostedOwnerPending, type HostedOwnerPendingStore} from '../src/services/owner-pending.ts';

// A provider sign-in or sign-up leaves for Google or Apple and comes back through Hosted's /account
// callback. One that began on a server's claim (setup or owner move) goes back to /claim, where the
// tab's own encrypted pending store restores it; everything else resumes Start as before.
const origin = 'https://account.example';
const claimCode = 'claim_' + Buffer.alloc(32, 5).toString('base64url');
function claimCapture(operation: string, server: string, at = origin) {
  const href = at + '/claim#' + new URLSearchParams({code: claimCode, operation, server});
  const page = {href, pathname: '/claim', search: '', hash: new URL(href).hash};
  return captureHostedOwnerClaim(page, {state: null, replaceState: () => {page.hash = '';}}, at)!;
}
function vault() {
  let saved: HostedOwnerPending | undefined;
  const store: HostedOwnerPendingStore = {assertAvailable: () => {}, read: async () => saved, change: async update => {saved = update(saved);}};
  return {store, saved: () => saved};
}
async function resumed(mode: 'login' | 'link', store?: HostedOwnerPendingStore) {
  const h = createRenderer(), paths: string[] = [];
  const location = Object.getOwnPropertyDescriptor(globalThis, 'location'), history = Object.getOwnPropertyDescriptor(globalThis, 'history');
  Object.defineProperty(globalThis, 'location', {configurable: true, value: {origin, href: origin + '/account?tab=security'}});
  Object.defineProperty(globalThis, 'history', {configurable: true, value: {replaceState: (_state: unknown, _title: string, path: string) => {paths.push(path);}}});
  try {
    const {AccountSecurityReturnEntry} = await componentModule(new URL('../src/services/AccountSecurityReturnEntry.tsx', import.meta.url), {
      react: h.react, './i18n': {servicesI18n: () => ({t: (key: string) => key})}, './ServicesApp': {ServicesApp: 'ServicesApp'},
      '../platform/account-security-journal': {browserSecurityJournal: () => ({read: async () => undefined})},
      '../platform/account-security': {originalSecurityClient: () => ({transaction: async () => ({mode, status: 'verified'})})},
      './owner-pending': {providerReturnPath, hostedOwnerPendingStore: () => {if (!store) throw new Error('secure_context_required'); return store;}},
    });
    const render = () => tree(h.render(() => AccountSecurityReturnEntry({arrival: {kind: 'transaction', transactionId: 'oid_' + 'a'.repeat(43)}})));
    let ordinary: any;
    render(); for (let i = 0; i < 200 && !ordinary; i++) {await new Promise(resolve => setTimeout(resolve, 0)); ordinary = render().find(node => node.type === 'ServicesApp');}
    assert.ok(ordinary, 'the ordinary consumer opens');
    assert.equal(ordinary.props.identityTransaction, mode === 'link' ? 'oid_' + 'a'.repeat(43) : undefined);
    return paths;
  } finally {
    h.dispose();
    if (location) Object.defineProperty(globalThis, 'location', location); else Reflect.deleteProperty(globalThis, 'location');
    if (history) Object.defineProperty(globalThis, 'history', history); else Reflect.deleteProperty(globalThis, 'history');
  }
}

test('a checked ordinary provider login resumes Start; linking keeps the Security consumer', async () => {
  assert.deepEqual(await resumed('login', vault().store), ['/'], 'a plain sign-up or sign-in, with nothing held, opens Start (and a member return held for Start resumes there)');
  assert.deepEqual(await resumed('login'), ['/'], 'no usable store: Start, as before');
  assert.deepEqual(await resumed('link', vault().store), []);
});

test('a provider sign-up that began on a setup claim or an owner move comes back to that claim', async () => {
  for (const [operation, server] of [['setup_operation', 'New server'], ['owner_move_operation', 'Moving server']]) {
    const f = vault();
    await restoreHostedOwnerPending(f.store, origin, claimCapture(operation, server));
    assert.deepEqual(await resumed('login', f.store), ['/claim'], operation);
    assert.equal(f.saved()?.request.operation, operation, 'the claim stays in the tab store for /claim to restore');
  }
});

test('a stale or foreign pending claim is not a destination: it is cleared and Start opens', async () => {
  const stale = vault();
  // Captured 31 minutes ago: saved, then already past its local budget.
  await restoreHostedOwnerPending(stale.store, origin, claimCapture('setup_operation', 'Old server'), Date.now() - 1_900_000).catch(() => undefined);
  assert.ok(stale.saved(), 'the stale claim is in the store');
  assert.deepEqual(await resumed('login', stale.store), ['/']);
  assert.equal(stale.saved(), undefined, 'an expired claim is cleared');
  const foreign = vault();
  await restoreHostedOwnerPending(foreign.store, 'https://elsewhere.example', claimCapture('setup_operation', 'Other', 'https://elsewhere.example'));
  assert.deepEqual(await resumed('login', foreign.store), ['/'], 'a claim for another Services origin never sends this page anywhere');
});

test('the login root keeps a pending server return, and otherwise opens the server/start destination', async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'location');
  Object.defineProperty(globalThis, 'location', {configurable: true, value: {pathname: '/', search: ''}});
  try {
    for (const returningToServer of [false, true]) {
      const h = createRenderer(); let Root!: () => unknown;
      const session = {account: {id: 'qa'}, familyId: 'qa-family'};
      const account = {service: {getSnapshot: () => ({phase: 'ready', session}), subscribe: () => () => {}}};
      const bootstrap = {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready', ...(returningToServer ? {signIn: {serverName: 'Original server'}} : {})})};
      const context = {account, bootstrap, invalidContinuation: false}, routes = new Map<string, {component: () => unknown}>();
      const root = {useRouteContext: () => context};
      await componentModule(new URL('../src/services/ServicesApp.tsx', import.meta.url), {
        react: h.react, './i18n': {servicesI18n: () => ({t: (key: string) => key})},
        '../shell/DocumentBootReady': {DocumentBootReady: ({children}: {children: unknown}) => children},
        './HomePage': {ServicesHomePage: 'StartOrServers'}, './SignInPage': {RegisteredSignInPage: 'OriginalServerReturn'},
        '@tanstack/react-router': {
          createRootRouteWithContext: () => (options: {component: () => unknown}) => {Root = options.component; return root;},
          createRoute: (route: {path: string; component: () => unknown}) => {routes.set(route.path, route); return route;},
          Outlet: () => h.react.createElement(routes.get('/')!.component),
        },
      });
      try {
        const shown = tree(h.render(Root));
        assert.ok(shown.some(node => node.type === (returningToServer ? 'OriginalServerReturn' : 'StartOrServers')));
      } finally {h.dispose();}
    }
  } finally {if (previous) Object.defineProperty(globalThis, 'location', previous); else Reflect.deleteProperty(globalThis, 'location');}
});

test('provider failures stay visible with an active family or an original server sign-in', async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'location');
  Object.defineProperty(globalThis, 'location', {configurable:true,value:{pathname:'/',search:''}});
  try {for(const active of [true,false]){
    const h=createRenderer();let Root!:()=>unknown;
    let session=active?{account:{id:'original'},familyId:'original-family'}:undefined;
    const pending={serverName:'Original server'};
    const context={providerFailure:'google',invalidContinuation:false,account:{service:{getSnapshot:()=>({phase:active?'ready':'signedOut',session}),subscribe:()=>()=>{}}},bootstrap:{subscribe:()=>()=>{},getSnapshot:()=>({phase:'ready',signIn:pending})}};
    const root={useRouteContext:()=>context};
    await componentModule(new URL('../src/services/ServicesApp.tsx',import.meta.url),{
      react:h.react,'./i18n':{servicesI18n:()=>({t:(key:string)=>key})},
      '../shell/DocumentBootReady':{DocumentBootReady:({children}:{children:unknown})=>children},
      '../kit/accounts/ServicesAuth':Object.fromEntries(['ServicesAuthStage','ServicesHeading','ServicesNotice','ServicesActions'].map(k=>[k,k])),
      '../kit/controls':{Button:'Button'},'./SignInPage':{RegisteredSignInPage:'OriginalServerReturn'},
      '@tanstack/react-router':{createRootRouteWithContext:()=>(options:{component:()=>unknown})=>{Root=options.component;return root;},createRoute:(r:unknown)=>r},
    });
    try{
      const shown=tree(h.render(Root));
      if(active){assert.ok(shown.some(n=>n.type==='ServicesNotice'));assert.equal(context.account.service.getSnapshot().session,session);}
      else{const form=shown.find(n=>n.type==='OriginalServerReturn');assert.equal(form?.props.providerFailure,'google');assert.equal(form?.props.pending,pending);
        session={account:{id:'new-account'},familyId:'new-family'};
        const accepted=tree(h.render(Root));const resumed=accepted.find(n=>n.type==='OriginalServerReturn');
        assert.equal(resumed?.props.providerFailure,undefined);assert.equal(resumed?.props.pending,pending);assert.equal(accepted.some(n=>n.type==='ServicesNotice'),false);
      }
    }finally{h.dispose();}
  }}finally{if(previous)Object.defineProperty(globalThis,'location',previous);else Reflect.deleteProperty(globalThis,'location');}
});


test('a provider return explicitly holds boot while the signed-out coordinator is ready', async()=>{
  const previous=Object.getOwnPropertyDescriptor(globalThis,'location');
  Object.defineProperty(globalThis,'location',{configurable:true,value:{pathname:'/',search:''}});
  const h=createRenderer();let Root!:()=>unknown;const ready:unknown[]=[];
  const context={providerReturn:true,invalidContinuation:false,account:{service:{subscribe:()=>()=>{},getSnapshot:()=>({phase:'signedOut'})}},bootstrap:{subscribe:()=>()=>{},getSnapshot:()=>({phase:'ready'})}};
  try{
    await componentModule(new URL('../src/services/ServicesApp.tsx',import.meta.url),{
      react:h.react,'./i18n':{servicesI18n:()=>({t:(key:string)=>key})},
      '../shell/DocumentBootReady':{DocumentBootReady:({ready: value,children}:{ready?:boolean;children:unknown})=>{ready.push(value);return children;}},
      '@tanstack/react-router':{createRootRouteWithContext:()=> (options:{component:()=>unknown})=>{Root=options.component;return {useRouteContext:()=>context};},createRoute:(x:unknown)=>x,Outlet:()=>null},
    });
    h.render(Root);assert.deepEqual(ready,[false],'undefined would enable DocumentBootReady default=true');
  }finally{h.dispose();if(previous)Object.defineProperty(globalThis,'location',previous);else Reflect.deleteProperty(globalThis,'location');}
});
