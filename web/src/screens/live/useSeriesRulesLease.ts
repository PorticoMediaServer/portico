import {forwardIdentity} from '../detail/context';
import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {ScreenContext} from '../detail/context';
import {createSeriesRulesBindings, type SeriesRulesBindings} from './series-rules-bindings';

export type SeriesRulesLease = {
  id: string;
  context: ScreenContext;
  live: boolean;
  /** The exact rules route witness and this opening's own live flag. */
  current: () => boolean;
  binding: SeriesRulesBindings;
};

/** One committed Series rules door. The exact context and actual rules route
 * own the binding and its request life; layout retirement retires them before
 * abort, so replay or a same-path destination change cannot revive the old
 * binding before its cleanup. */
export function useSeriesRulesLease(context: ScreenContext | undefined, routeCurrent: () => boolean): SeriesRulesLease | undefined {
  const [published, publish] = useState<SeriesRulesLease>();
  // The route witness belongs to this exact opening: keep the one this effect ran
  // with instead of recreating the binding whenever the route hands a new closure.
  const route = useRef(routeCurrent);
  useLayoutEffect(() => {route.current = routeCurrent;});
  useLayoutEffect(() => {
    const routeCurrent = route.current;
    if (!context || !context.isCurrent() || !routeCurrent()) return;
    let held: SeriesRulesLease | undefined;
    const current = () => !!held && held.live && context.isCurrent() && routeCurrent();
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const binding = createSeriesRulesBindings(screen, {locale: defaultI18n.region.formatLocale,
      timezone: defaultI18n.region.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone});
    const lease: SeriesRulesLease = {id: crypto.randomUUID(), context, live: true, current, binding};
    held = lease;
    publish(lease);
    void binding.load().catch(() => {});
    return () => {lease.live = false;binding.dispose();};
    // routeCurrent is captured by this exact rules opening.
  }, [context]);
  return published?.live && published.context === context && published.current() ? published : undefined;
}
