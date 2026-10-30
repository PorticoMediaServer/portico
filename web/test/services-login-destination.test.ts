import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

test('a checked ordinary provider login resumes Start; linking keeps the Security consumer', async () => {
  for (const mode of ['login', 'link']) {
    const h = createRenderer(), paths: string[] = [];
    const location = Object.getOwnPropertyDescriptor(globalThis, 'location'), history = Object.getOwnPropertyDescriptor(globalThis, 'history');
    Object.defineProperty(globalThis, 'location', {configurable: true, value: {origin: 'https://account.example', href: 'https://account.example/account?tab=security'}});
    Object.defineProperty(globalThis, 'history', {configurable: true, value: {replaceState: (_state: unknown, _title: string, path: string) => {paths.push(path);}}});
    try {
      const {AccountSecurityReturnEntry} = await componentModule(new URL('../src/services/AccountSecurityReturnEntry.tsx', import.meta.url), {
        react: h.react, './i18n': {servicesI18n: () => ({t: (key: string) => key})}, './ServicesApp': {ServicesApp: 'ServicesApp'},
        '../platform/account-security-journal': {browserSecurityJournal: () => ({read: async () => undefined})},
        '../platform/account-security': {originalSecurityClient: () => ({transaction: async () => ({mode, status: 'verified'})})},
      });
      const render = () => tree(h.render(() => AccountSecurityReturnEntry({arrival: {kind: 'hosted', captured: {kind: 'transaction', transactionId: 'oid_' + 'a'.repeat(43)}}})));
      render(); for (let i = 0; i < 12; i++) await Promise.resolve();
      const ordinary = render().find(node => node.type === 'ServicesApp');
      assert.ok(ordinary);
      assert.deepEqual(paths, mode === 'login' ? ['/'] : []);
      assert.equal(ordinary.props.identityTransaction, mode === 'link' ? 'oid_' + 'a'.repeat(43) : undefined);
    } finally {
      h.dispose();
      if (location) Object.defineProperty(globalThis, 'location', location); else Reflect.deleteProperty(globalThis, 'location');
      if (history) Object.defineProperty(globalThis, 'history', history); else Reflect.deleteProperty(globalThis, 'history');
    }
  }
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
