import React from 'react';
import {View} from 'react-native';
import {tvAccountSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAuthLayout} from './TVAuthLayout';

export type TVFrontDoorProps = Readonly<{
  busy: boolean; error?: React.ReactNode;
  onPortico: () => void; onLocal: () => void;
}>;

/** connect40's signed-out screen. The account owner alone starts either flow;
 * rendering this screen makes no Services request and installs no Back action. */
export function TVFrontDoor(props: TVFrontDoorProps) {
  return <FocusProvider><FrontDoor {...props}/></FocusProvider>;
}
function FrontDoor({busy, error, onPortico, onLocal}: TVFrontDoorProps) {
  const id = 'tv.auth.front-door';
  const entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision}, {regionId: id, childId: 'portico'});
  return <TVAuthLayout testID="tv-front-door" title={defaultI18n.t('web.signin.welcomeHome')}>
    {error && <View accessibilityRole="alert" style={{marginBottom: tvAccountSpec.actionGap}}>{error}</View>}
    <FocusRegion id={id} entry="portico" order={['portico', 'local']} orientation="horizontal" enabled={!busy}
      style={{flexDirection: 'row', gap: tvAccountSpec.actionGap}}>
      <Button focusId="portico" testID="tv-front-door-portico" disabled={busy} onPress={() => {if (!busy) onPortico();}}>{defaultI18n.t('accounts.signIn.portico')}</Button>
      <Button focusId="local" testID="tv-front-door-local" variant="glass" disabled={busy} onPress={() => {if (!busy) onLocal();}}>{defaultI18n.t('accounts.signIn.local')}</Button>
    </FocusRegion>
  </TVAuthLayout>;
}
