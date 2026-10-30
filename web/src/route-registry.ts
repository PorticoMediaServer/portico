import type {ComponentType} from 'react';
import type {ShellPlaybackBinding} from './shell/Playback';
import {accountReturnPath} from './route-registry-return';

/** Stable destinations. Screen owners supply their own component in *.route.tsx. */
export const webScreenPaths = Object.freeze({
  home: '/', search: '/search', readingSearch: '/search/reading', libraries: '/libraries', library: '/library/$libraryId',
  live: '/live', saved: '/saved', item: '/item/$itemId', homeRow: '/home/rows/$rowId',
  player: '/player', settings: '/settings', dashboard: '/dashboard', notifications: '/notifications',
  setup: '/setup', profiles: '/profiles', servers: '/servers', account: '/account', signIn: '/sign-in',
  together: '/watch-together', help: '/help', about: '/about',
  person: '/person/$personId', read: '/read/$itemId', device: '/device',
} as const);
export type WebScreenId = keyof typeof webScreenPaths;
export type WebScreenChrome = 'shell' | 'standalone' | 'player';
export type WebScreenSearch = Record<string, unknown>;
export type WebScreenSearchValidator = (raw: WebScreenSearch) => WebScreenSearch;
export type WebScreenDefinition = Readonly<{
  id: WebScreenId;
  component: ComponentType;
  chrome?: WebScreenChrome;
  /** Compact phone widths may need the screen to own its bar instead of the global tab bar. */
  phoneChrome?: 'shell' | 'standalone';
  /** Own the query schema here. The router never forwards unvalidated raw keys. */
  validateSearch?: WebScreenSearchValidator;
  /** The player module's one persistent media owner, mounted outside route outlets. */
  playbackHost?: ComponentType<{binding: ShellPlaybackBinding | undefined}>;
  /** A screen-owned protected lifetime that may survive an auxiliary route.
   * The component owns visibility and starts work only for its actual destination. */
  retainedHost?: ComponentType;
}>;
export function defineWebScreen<T extends WebScreenDefinition>(definition: T): Readonly<T> {
  if (!Object.hasOwn(webScreenPaths, definition.id)) throw new Error('unknown-web-screen');
  if (definition.phoneChrome !== undefined && !['shell', 'standalone'].includes(definition.phoneChrome)) throw new Error('invalid-phone-chrome');
  if (definition.validateSearch !== undefined && typeof definition.validateSearch !== 'function') throw new Error('invalid-web-search-validator');
  if (definition.playbackHost && definition.id !== 'player') throw new Error('playback-host-must-belong-to-player');
  if (definition.retainedHost && definition.id === 'player') throw new Error('player-already-has-playback-host');
  return Object.freeze({...definition});
}
export type WebActionId = 'feedback';
export type WebActionDefinition = Readonly<{
  kind: 'action'; id: WebActionId; host: ComponentType;
  subscribe: (listener: () => void) => () => void;
  /** The action owner projects genuine canSubmit and captured currentness. */
  getSnapshot: () => boolean;
  /** Menu entries have no target. Rich context stays in the domain's typed API. */
  invoke: () => void;
}>;
export function defineWebAction(definition: Omit<WebActionDefinition, 'kind'>): WebActionDefinition {
  if (definition.id !== 'feedback') throw new Error('unknown-web-action');
  if (typeof definition.subscribe !== 'function' || typeof definition.getSnapshot !== 'function' || typeof definition.invoke !== 'function') throw new Error('invalid-web-action');
  return Object.freeze({...definition, kind: 'action'});
}
/** Availability is reread on the gesture, so an old menu closure grants nothing. */
export function invokeWebAction(action: WebActionDefinition | undefined, isCurrent: () => boolean): boolean {
  if (!isCurrent() || action?.getSnapshot() !== true) return false;
  action.invoke(); return true;
}
type WebRegistration = WebScreenDefinition | WebActionDefinition;
export type WebScreenModule = Readonly<{default: WebRegistration | readonly WebRegistration[]}>;
const isAction = (value: WebRegistration): value is WebActionDefinition => 'kind' in value && value.kind === 'action';
/** Duplicate owners fail at startup rather than silently replacing a registered screen. */
export function createWebScreenRegistry(modules: Readonly<Record<string, WebScreenModule>>) {
  const definitions = new Map<WebScreenId, WebScreenDefinition>();
  const actions = new Map<WebActionId, WebActionDefinition>();
  for (const filename of Object.keys(modules).sort()) {
    const exported = modules[filename].default;
    const values: readonly WebRegistration[] = Array.isArray(exported) ? exported : [exported as WebRegistration];
    for (const value of values) {
      if (isAction(value)) {
        const action = defineWebAction(value);
        if (actions.has(action.id)) throw new Error(`duplicate-web-action:${action.id}`);
        actions.set(action.id, action); continue;
      }
      const definition = defineWebScreen(value);
      if (definitions.has(definition.id)) throw new Error(`duplicate-web-screen:${definition.id}`);
      definitions.set(definition.id, definition);
    }
  }
  return Object.freeze({
    get: (id: WebScreenId) => definitions.get(id),
    getAction: (id: WebActionId) => actions.get(id),
    actions: () => [...actions.values()],
    retainedHosts: () => [...definitions.values()].filter(value => value.retainedHost).map(value => ({id: value.id, component: value.retainedHost!})),
    validateSearch: (id: WebScreenId, raw: WebScreenSearch, fallback: WebScreenSearchValidator = () => ({})): WebScreenSearch => {
      const parsed = (definitions.get(id)?.validateSearch ?? fallback)(raw);
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || ![Object.prototype, null].includes(Object.getPrototypeOf(parsed))) throw new Error('invalid-web-search-result');
      return parsed;
    },
    missing: () => (Object.keys(webScreenPaths) as WebScreenId[]).filter(id => !definitions.has(id)),
    chrome: (id: WebScreenId): WebScreenChrome => definitions.get(id)?.chrome
      ?? (['setup', 'signIn', 'profiles', 'servers', 'account', 'read', 'device'].includes(id) ? 'standalone' : id === 'player' ? 'player' : 'shell'),
    phoneChrome: (id: WebScreenId): 'shell' | 'standalone' => definitions.get(id)?.phoneChrome ?? 'shell',
  });
}
export function webScreenIdForRoute(routeId: string): WebScreenId | undefined {
  if (routeId === accountReturnPath) return 'signIn';
  return (Object.keys(webScreenPaths) as WebScreenId[]).find(id => webScreenPaths[id] === routeId);
}
