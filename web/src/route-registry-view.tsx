import {createContext, useCallback, useContext, useSyncExternalStore, type ReactNode} from 'react';
import {createWebScreenRegistry, invokeWebAction, type WebActionId, type WebScreenId, type WebScreenModule} from './route-registry';
import type {ShellBindings} from './shell/bindings';
import type {ShellAssemblyContext, ShellBootstrap} from './shell/bootstrap';
import type {TakeAccountReturn} from './route-registry-return';
import type {TakeAccountMove} from './route-registry-arrival';

// Eager discovery imports registrations once. Components/services still mount lazily
// in their own route; importing a module must not start requests or create a runtime.
export const webScreens = createWebScreenRegistry({
  ...import.meta.glob<WebScreenModule>('./screens/**/*.route.tsx', {eager: true}),
  ...import.meta.glob<WebScreenModule>('./screens/**/*.action.tsx', {eager: true}),
});
export type WebScreenContext = Readonly<{
  binding?: ShellBindings;
  /** The actual captured server/profile epoch, with its original live PreferencesReader. */
  context?: ShellAssemblyContext;
  bootstrap?: ShellBootstrap;
  /** One captured callback, scrubbed before startup. The protected journal decides KIND. */
  takeAccountReturn?: TakeAccountReturn;
  /** Original scrubbed move link, delivered once; entry proves its expected ID. */
  takeAccountMove?: TakeAccountMove;
}>;
const Context = createContext<WebScreenContext>({});
export function WebScreenProvider({value, children}: {value: WebScreenContext; children: ReactNode}) {
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useWebScreenContext(): WebScreenContext {return useContext(Context);}
export function useProtectedWebScreenContext() {
  const value = useWebScreenContext();
  return value.binding && value.context?.isCurrent() ? {...value, binding: value.binding, context: value.context} : undefined;
}
const noActionSubscribe = () => () => {};
const noActionSnapshot = () => false;
/** The action owner supplies canSubmit; the shared boundary only fences scope. */
export function useWebAction(id: WebActionId): (() => void) | undefined {
  const current = useProtectedWebScreenContext(), context = current?.context, action = webScreens.getAction(id);
  const available = useSyncExternalStore(action?.subscribe ?? noActionSubscribe, action?.getSnapshot ?? noActionSnapshot, noActionSnapshot);
  const invoke = useCallback(() => {invokeWebAction(action, () => context?.isCurrent() === true);}, [action, context]);
  return current && available ? invoke : undefined;
}
/** Route components use the current captured epoch as their lifetime key. Token
 * rotation and the first Home response keep the same component and services. */
export function WebScreenOutlet({id, fallback}: {id: WebScreenId; fallback?: ReactNode}) {
  const {binding} = useWebScreenContext(), Screen = webScreens.get(id)?.component;
  return Screen ? <Screen key={binding?.scopeKey ?? 'signed-out'} /> : fallback;
}

/** Inside the router but outside changing shell/standalone chrome. A real
 * authority replacement unmounts every retained lifetime with its captured scope. */
export function WebRetainedScreenHosts() {
  const current = useProtectedWebScreenContext();
  if (!current) return null;
  return <>
    {webScreens.retainedHosts().map(({id, component: Host}) => <Host key={JSON.stringify([id, current.binding.scopeKey])} />)}
    {webScreens.actions().map(({id, host: Host}) => <Host key={JSON.stringify(['action', id, current.binding.scopeKey])} />)}
  </>;
}
