import React from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import type {NativeShellRoute} from '../../shell/routes';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {openNativeReadingSettings} from '../settings/ReadingSettingsRoute';
import {isReadingLibraryKind} from './platform';
import {NativeReadBook} from './read-book-model';
import {NativeReadPeriodical} from './read-periodical-model';
import {NativeReadSeries} from './read-series-model';
import {ReadBookScreen} from './ReadBookScreen';
import {ReadPeriodicalScreen} from './ReadPeriodicalScreen';
import {ReadSeriesScreen} from './ReadSeriesScreen';

/** phone-read 3/3c and 4/4c: route-owned book and series destinations. The cache
 * holds inert controllers keyed by the exact route object, so reopening an item
 * through a new descriptor gets its own controller while the retained origin
 * keeps its original one for Back. Each page arms and loads from its own layout
 * effect with the route it was rendered under. The reader still opens through the
 * one existing read route/host. */
type BookRoute = Extract<NativeShellRoute, {kind: 'read-book'}>;
type SeriesRoute = Extract<NativeShellRoute, {kind: 'read-series'}>;
type PeriodicalRoute = Extract<NativeShellRoute, {kind: 'read-periodical'}>;

/** A route may only render while its library is still a current authorized
 * Reading library and the descriptor still carries the same real ids. */
function readingLibrary(context: NativeShellBootstrapContext, libraryId: string) {
  if (!context.current() || !context.session()) return undefined;
  const library = context.libraries().find(candidate => candidate.id === libraryId);
  return library && isReadingLibraryKind(library.kind) ? library : undefined;
}
const bookRouteCurrent = (context: NativeShellBootstrapContext, route: BookRoute): boolean =>
  !!readingLibrary(context, route.libraryId) && route.item.libraryId === route.libraryId
  && typeof route.item.id === 'string' && route.item.id.length > 0
  && typeof route.item.sourceId === 'string' && route.item.sourceId.length > 0;
const seriesRouteCurrent = (context: NativeShellBootstrapContext, route: SeriesRoute): boolean =>
  !!readingLibrary(context, route.libraryId) && /^[A-Za-z0-9_-]{43}$/.test(route.seriesId);
const periodicalRouteCurrent = (context: NativeShellBootstrapContext, route: PeriodicalRoute): boolean => {
  const library = readingLibrary(context, route.libraryId);
  return !!library && library.kind === 'book' && /^[A-Za-z0-9_-]{43}$/.test(route.groupId)
    && (route.publicationType === 'magazine' || route.publicationType === 'newspaper');
};

function retain<T extends {dispose: () => void}>(pages: Map<object, T>, key: object, value: T): void {
  pages.set(key, value);
  while (pages.size > 3) {
    const oldest = pages.keys().next();
    if (oldest.done) return;
    const stale = pages.get(oldest.value);
    pages.delete(oldest.value);
    stale?.dispose();
  }
}

export function createNativeReadBookRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context);
  const pages = new Map<BookRoute, NativeReadBook>();
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const controllerFor = (route: BookRoute): NativeReadBook | undefined => {
    if (!current() || !bookRouteCurrent(context, route)) return undefined;
    const cached = pages.get(route);
    if (cached) return cached;
    const page = new NativeReadBook(context, route);
    retain(pages, route, page);
    return page;
  };
  const renderer: NativeRouteRenderer = {
    render: (route, runtime) => {
      if (route.kind !== 'read-book') return null;
      const controller = controllerFor(route);
      if (!controller) return null;
      const library = readingLibrary(context, route.libraryId);
      if (!library) return null;
      const role = context.session()?.viewer.role;
      return <ReadBookScreen controller={controller} route={route} comic={library.kind === 'comic'} context={context}
        owner={role === 'owner'} runtime={runtime}
        onBack={() => {if (current() && context.navigation.getSnapshot().route === route) context.navigation.back(context.authority());}}
        onOpenSettings={() => {if (current() && context.navigation.getSnapshot().route === route) openNativeReadingSettings(context);}}/>;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      for (const page of pages.values()) page.dispose();
      pages.clear();
    },
  };
  return Object.freeze(renderer);
}

export function createNativeReadSeriesRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context);
  const pages = new Map<SeriesRoute, NativeReadSeries>();
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const controllerFor = (route: SeriesRoute): NativeReadSeries | undefined => {
    if (!current() || !seriesRouteCurrent(context, route)) return undefined;
    const cached = pages.get(route);
    if (cached) return cached;
    const library = readingLibrary(context, route.libraryId);
    if (!library) return undefined;
    const page = new NativeReadSeries(context, route, library.kind === 'comic');
    retain(pages, route, page);
    return page;
  };
  const renderer: NativeRouteRenderer = {
    render: (route, runtime) => {
      if (route.kind !== 'read-series') return null;
      const controller = controllerFor(route);
      const library = readingLibrary(context, route.libraryId);
      if (!controller || !library) return null;
      return <ReadSeriesScreen controller={controller} route={route} comic={library.kind === 'comic'} context={context} runtime={runtime}
        onBack={() => {if (current() && context.navigation.getSnapshot().route === route) context.navigation.back(context.authority());}}/>;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      for (const page of pages.values()) page.dispose();
      pages.clear();
    },
  };
  return Object.freeze(renderer);
}

export function createNativeReadPeriodicalRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context);
  const pages = new Map<PeriodicalRoute, NativeReadPeriodical>();
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const controllerFor = (route: PeriodicalRoute): NativeReadPeriodical | undefined => {
    if (!current() || !periodicalRouteCurrent(context, route)) return undefined;
    const cached = pages.get(route);
    if (cached) return cached;
    const page = new NativeReadPeriodical(context, route);
    retain(pages, route, page);
    return page;
  };
  const renderer: NativeRouteRenderer = {
    render: (route, runtime) => {
      if (route.kind !== 'read-periodical') return null;
      const controller = controllerFor(route);
      if (!controller) return null;
      return <ReadPeriodicalScreen controller={controller} route={route} context={context} runtime={runtime}
        onBack={() => {if (current() && context.navigation.getSnapshot().route === route) context.navigation.back(context.authority());}}/>;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      for (const page of pages.values()) page.dispose();
      pages.clear();
    },
  };
  return Object.freeze(renderer);
}

nativeShellRoutes.registerRoute('read-book', createNativeReadBookRenderer);
nativeShellRoutes.registerRoute('read-series', createNativeReadSeriesRenderer);
nativeShellRoutes.registerRoute('read-periodical', createNativeReadPeriodicalRenderer);
