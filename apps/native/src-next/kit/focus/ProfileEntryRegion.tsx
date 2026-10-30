import React, {useMemo} from 'react';
import {FocusProvider, FocusRegion, useFocusRegistry} from './FocusRegion';
import {createDestinationEntryFocus} from './destination-entry';
import {useDestinationEntryFocus} from './useDestinationEntryFocus';

type Props = {scope: string; entry?: string; order: readonly string[]; children: React.ReactNode};
/** One explicit profile-page opening; native guides own every directional move. */
export function ProfileEntryRegion(props: Props) {
  return <FocusProvider><Region {...props}/></FocusProvider>;
}
function Region({scope, entry, order, children}: Props) {
  const registry = useFocusRegistry();
  const opening = useMemo(() => ({scope, request: createDestinationEntryFocus(registry)}), [registry, scope]);
  useDestinationEntryFocus({visible: true, intent: 'enter', revision: 0}, entry ? {regionId: opening.scope, childId: entry} : undefined, undefined, opening.request);
  return <FocusRegion id={scope} enabled={order.length > 0} entry={entry} order={order} orientation="horizontal" style={{alignSelf: 'stretch'}}>{children}</FocusRegion>;
}
