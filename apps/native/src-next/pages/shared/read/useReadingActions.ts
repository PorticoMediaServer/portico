import {useEffect, useLayoutEffect, useMemo, useSyncExternalStore} from 'react';
import type {CardSelection} from '../../../kit/cards/model';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeReadingActions, type NativeReadingActionOffer} from '../../../screens/read/reading-actions';

export function useReadingActions(context: NativeShellBootstrapContext, runtime: NativeShellRuntime, owner: object) {
  const actions = useMemo(() => new NativeReadingActions(context, runtime.reportError), [context, runtime.reportError]);
  useLayoutEffect(() => {actions.arm(); return () => actions.release();}, [actions, owner]);
  // Navigation clears the opening; the retained owner can capture its route again on Back.
  useEffect(() => actions.reconcile());
  const state = useSyncExternalStore(actions.subscribe, actions.getSnapshot);
  const selection = (offer: NativeReadingActionOffer): CardSelection => ({active: state.selecting, selected: actions.selected(offer.item),
    onToggle: () => actions.toggle(offer), onEnter: () => actions.toggle(offer)});
  return {actions, state, selection};
}
export type ReadingActionsPort = ReturnType<typeof useReadingActions>;

