import {dependsOn, type FreshnessHub, type FreshDocument} from '@core/freshness-hub.ts';
import type {HomeService} from '@core/home-service.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';

export const homeDependencies = [dependsOn.libraries, dependsOn.content(), dependsOn.order(), dependsOn.personal(), dependsOn.progress(),
  dependsOn.settings('preferences'), dependsOn.settings('server'), dependsOn.list('recommendations'), dependsOn.list('guide'),
  dependsOn.list('liveSources'), dependsOn.list('libraryChannels'), dependsOn.clock('home-time')];

export class HomeReadNotAnswered extends Error {
  readonly retryable = true;
  constructor() {super('Home read was not adopted.'); this.name = 'HomeReadNotAnswered';}
}

/** Shell owns this display registration and the service; the route only opens/closes it. */
export function createShellHomeDocument(hub: FreshnessHub, service: HomeService) {
  const document = hub.document({dependencies: homeDependencies,
    hasContent: () => !!service.getSnapshot().document,
    revalidate: async context => {
      const result = await service.refresh(context);
      if (result !== 'adopted') throw new HomeReadNotAnswered();
    },
    recover: async context => {
      if (context.signal.aborted) throw new HomeReadNotAnswered();
      await service.load();
      const snapshot = service.getSnapshot();
      if (!snapshot.document || snapshot.phase !== 'ready') throw snapshot.error ?? new HomeReadNotAnswered();
    }});
  let failed = false;
  const observe = () => {
    const snapshot = service.getSnapshot(), firstFailure = !snapshot.document && snapshot.phase === 'error';
    if (firstFailure && !failed) document.invalidate();
    failed = firstFailure; document.check();
  };
  const off = service.subscribe(observe); observe();
  return {document, dispose: () => {off(); document.dispose();}};
}

/** Preference adoption owes display work, never bypasses the route's open gate. */
export function watchShellHomePreferences(o: Readonly<{
  read(): PreferenceSnapshot; subscribe(listener: () => void): () => void;
  document: FreshDocument; isCurrent(): boolean; refreshSettings(): void;
  authority(): void; changed(): void;
}>) {
  const layoutKey = () => JSON.stringify([o.read().effective['home.rowOrder'], o.read().effective['home.hiddenRowIds']]);
  let layout = layoutKey();
  return o.subscribe(() => {
    if (!o.isCurrent()) return;
    o.refreshSettings();
    const next = layoutKey();
    if (next !== layout) {layout = next; o.document.invalidate(); o.authority();}
    o.changed();
  });
}
