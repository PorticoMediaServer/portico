import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {NativeModules, Platform} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {PersonalSavedService, readPersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import {StateView} from '../../kit/states';
import {HomeCustomize, type NativeHomeCustomizeCopy} from './HomeCustomize';
import {NativeHomeCustomizeController} from './customize-controller';
import {NativeHomeSavedViewSummaries} from './saved-view-summaries';
import {takeHomeCustomizeView, type HomeCustomizeViewIntent} from './customize-intent';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeShellRoute} from '../../shell/routes';
import {sameAuthority} from '../../shell/model';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';

type Source = Readonly<{route: NativeShellRoute; key: number; controller: NativeHomeCustomizeController; views: PersonalSavedService; summaries: NativeHomeSavedViewSummaries; current: () => boolean; initialIntent?: HomeCustomizeViewIntent; remember: (views: readonly PersonalResource[]) => void; readView: (id: string) => Promise<PersonalResource | undefined>}>;

/** Authority-local route factory. Registration belongs to this screen module;
 * the shell only binds its factories to the restored context. */
export function createNativeHomeCustomizeRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer & {dispose: () => void} {
  const screen = createNativeHomeCustomization(context);
  const offs = [context.navigation.subscribe(screen.reconcile), context.subscribeAuthority(screen.reconcile), context.home.subscribe(screen.reconcile)];
  screen.reconcile();
  return {
    render: (route, runtime) => {
      if (!context.current() || route.kind !== 'home-customize' || context.navigation.getSnapshot().route !== route) throw new Error('native_home_customize_route_mismatch');
      screen.reconcile(); return screen.render(runtime);
    },
    dispose: () => {offs.forEach(off => off());screen.dispose();},
  };
}

/** The drawn customize copy in the translator the runtime currently shows. */
function customizeCopy(text: NativeShellRuntime['text']): NativeHomeCustomizeCopy {
  return {
    title: text.t('home.customize.action'), instruction: text.t(Platform.isTV ? 'next.home.layoutTVInstruction' : 'home.customize.subtitle'),
    alwaysOn: text.t('home.customize.alwaysOn'), addView: text.t('home.customize.addView'), reset: text.t('action.reset'), save: text.t('action.save'),
    conflictTitle: text.t('next.home.layoutChangedTitle'), conflictBody: text.t('next.home.layoutChangedBodyCompact'),
    reload: text.t('next.home.reloadLayout'), keepEditing: text.t('next.home.keepEditing'),
    move: text.t('next.home.layoutMoveAction'), drop: text.t('next.home.layoutDropAction'), putBack: text.t('next.home.layoutPutBackAction'),
  };
}

/** The Home service performs the one revision-checked layout write. Saved is
 * read through its canonical service; choosing a view only edits the draft. */
export function createNativeHomeCustomization(context: NativeShellBootstrapContext) {
  let source: Source | undefined, alive = true, sequence = 0;
  let retireSource = () => {};
  const requestId = async (): Promise<string> => NativeModules.PorticoRuntime.newRequestId();
  const retire = () => {const stop = retireSource;retireSource = () => {};source = undefined;stop();};
  const reconcile = () => {
    if (!alive || !context.current()) {retire();return;}
    const route = context.navigation.getSnapshot().route;
    if (route === source?.route) {
      if (!source.current()) {const original = source;retire();if (context.current() && context.navigation.getSnapshot().route === original.route) context.navigation.back(context.authority());}
      return;
    }
    retire();if (route.kind !== 'home-customize') return;
    const session = context.session(), document = context.home.getSnapshot().document;
    if (!session || !document) return;
    const authority = {...context.authority()}, family = session.sessionFamilyId, owner = nativeDestinationLifetime(context);
    const abort = new AbortController();let live = true;
    const current = () => alive && live && !abort.signal.aborted && source?.key === key && owner.current()
      && sameAuthority(authority, context.authority()) && context.session()?.sessionFamilyId === family && context.navigation.getSnapshot().route === route
      && context.home.getSnapshot().document?.serverId === document.serverId && context.home.getSnapshot().document?.viewerFence === document.viewerFence;
    const key = ++sequence;
    const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!current()) throw Object.assign(new Error('Customize Home opening retired'), {name: 'AbortError'});
      const round = new AbortController(), stop = () => round.abort();
      abort.signal.addEventListener('abort', stop, {once: true});signal?.addEventListener('abort', stop, {once: true});if (signal?.aborted) stop();
      try {const value = await context.api.request<T>(path, method, body, round.signal);if (!current() || round.signal.aborted) throw Object.assign(new Error('Customize Home opening retired'), {name: 'AbortError'});return value;}
      finally {abort.signal.removeEventListener('abort', stop);signal?.removeEventListener('abort', stop);}
    }};
    const records = new Map<string, PersonalResource>();
    const eligible = (view: PersonalResource) => current() && view.serverId === authority.serverId && view.kind === 'view' && view.status === 'ready'
      && !!view.definition && view.actions.includes('pin') && context.libraries().some(library => library.id === view.definition!.libraryId);
    const views = new PersonalSavedService(api, owner.scope, requestId);
    const controller = new NativeHomeCustomizeController({home: context.home, current, requestId,
      savedViews: {serverId: authority.serverId, isCurrent: view => eligible(view) && records.get(view.id) === view}});
    const summaries = new NativeHomeSavedViewSummaries({api, serverId: authority.serverId, current, libraryIds: () => context.libraries().map(library => library.id)});
    const initialIntent = takeHomeCustomizeView(route, authority, family);
    const prune = (resources: readonly PersonalResource[]) => {
      const retained = new Set([...controller.getSnapshot().rows.filter(row => row.kind === 'saved_view' && row.id.startsWith('view:')).map(row => row.id.slice(5)), ...resources.map(view => view.id), ...(initialIntent ? [initialIntent.resourceId] : [])]);
      for (const [id, view] of records) if (!retained.has(id) || !eligible(view)) records.delete(id);
    };
    const remember = (resources: readonly PersonalResource[]) => {
      if (!current()) return;
      prune(resources);for (const view of resources) if (eligible(view)) records.set(view.id, view);
      controller.bindSavedViews([...records.values()]);
    };
    const stopViews = views.subscribe(() => remember(views.getSnapshot().resources));
    const stopRows = controller.subscribe(() => {if (current()) prune(views.getSnapshot().resources);});
    const readView = async (id: string) => {
      if (!current()) return;
      const view = await readPersonalResource(api, owner.scope, id, abort.signal);
      if (!eligible(view) || !controller.getSnapshot().rows.some(row => row.kind === 'saved_view' && row.id === `view:${id}`) && !views.getSnapshot().resources.some(row => row.id === id) && initialIntent?.resourceId !== id) return;
      records.set(view.id, view);controller.bindSavedViews([...records.values()]);return view;
    };
    source = {route, key, controller, views, summaries, current, initialIntent, remember, readView};
    retireSource = () => {live = false;abort.abort();stopViews();stopRows();controller.dispose();views.dispose();summaries.dispose();records.clear();};
  };
  return {reconcile, owns: (route: NativeShellRoute) => source?.route === route,
    render: (runtime: NativeShellRuntime) => {
      const opening = source;if (!opening?.current()) return null;
      return <Customization key={opening.key} source={opening} runtime={runtime} close={() => {if (opening.current()) context.navigation.back(context.authority());}}/>;
    }, dispose: () => {alive = false;retire();}};
}

function Customization({source, runtime, close}: {source: Source; runtime: NativeShellRuntime; close: () => void}) {
  const state = useSyncExternalStore(source.controller.subscribe, source.controller.getSnapshot), views = useSyncExternalStore(source.views.subscribe, source.views.getSnapshot);
  useSyncExternalStore(source.summaries.subscribe, source.summaries.getSnapshot);
  useEffect(() => {void source.controller.load();}, [source]);
  const summaryResources = useRef<readonly PersonalResource[]>([]);
  useEffect(() => {
    const rows = new Map<string, PersonalResource>();for (const view of views.resources) rows.set(view.id, view);
    for (const row of state.rows) if (row.savedView) rows.set(row.savedView.id, row.savedView);
    const next = [...rows.values()].sort((a, b) => a.id.localeCompare(b.id));
    if (next.length === summaryResources.current.length && next.every((view, index) => view === summaryResources.current[index])) return;
    summaryResources.current = next;void source.summaries.load(next);
  }, [source, views.resources, state.rows]);
  const retry = () => {void source.views.select({view: 'views'});};
  const labels = {t: runtime.text.t, has: defaultI18n.has, locale: runtime.text.region?.uiLocale ?? defaultI18n.locale}, copy = customizeCopy(runtime.text);
  const intentAdded = useRef(false);
  const [intentProblem, setIntentProblem] = useState<string>();
  const initialAttempt = useRef(false);
  useEffect(() => {
    const layout = state.layout;if (!layout) return;
    const ids = layout.rows.filter(row => row.kind === 'saved_view' && row.id.startsWith('view:')).map(row => row.id.slice(5));
    let active = true, next = 0;
    void Promise.all(Array.from({length: Math.min(4, ids.length)}, async () => {
      while (active && source.current()) {const id = ids[next++];if (!id) return;try {await source.readView(id);} catch { /* No summary is invented for unavailable metadata. */ }}
    }));
    return () => {active = false;};
  }, [source, state.layout]);
  useEffect(() => {
    const intent = source.initialIntent;if (!intent || initialAttempt.current || state.phase !== 'ready') return;
    initialAttempt.current = true;
    void source.readView(intent.resourceId).then(view => {
      if (!source.current()) return;
      if (!view || view.revision !== intent.revision || view.serverId !== intent.serverId || view.viewerFence !== intent.viewerFence) {setIntentProblem(runtime.text.t('error.unavailable'));return;}
      if (view.onHome) {intentAdded.current = true;return;}
      intentAdded.current = source.controller.addSavedView(view, Platform.isTV);
      if (!intentAdded.current) setIntentProblem(runtime.text.t('error.unavailable'));
    }, () => {if (source.current()) setIntentProblem(runtime.text.t('error.unavailable'));});
  }, [source, state.phase, runtime]);
  return <HomeCustomize summary={view => source.summaries.line(view, labels)} controller={source.controller} copy={copy} i18n={labels} onClose={close} onAddView={retry} notice={intentProblem} renderError={(error, again) => <StateView variant="centered" {...runtime.error(error, again, 'home')} testID="home-customize-error"/>} testID="home-customize"
      views={{items: views.resources, loading: views.loading, error: views.error, retry, hasMore: !!views.nextCursor,
        loadMore: () => {if (source.current()) void source.views.next().catch(error => {if (source.current()) runtime.reportError(error);});},
        renderError: (error, again) => <StateView variant="centered" {...runtime.error(error, again, 'home')} testID="home-customize-views-error"/>}}/>;
}

nativeShellRoutes.registerRoute('home-customize', createNativeHomeCustomizeRenderer);
