import React from 'react';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StateLayout, StateView} from '../../kit/states';
import {useTheme} from '../../kit/theme';

/** states16: an admitted member with no shared libraries waits on the server,
 * with an actual server switch available rather than an invented retry. */
export function NativeHomeEmpty({owner, serverName, hasLibraries, onSwitchServer}: {
  owner: boolean; serverName: string; hasLibraries: boolean; onSwitchServer?: () => void;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  if (theme.platform === 'phone' && !owner && !hasLibraries) return <StateLayout
    testID="native-home-nothing-shared" icon="i-clock" contentTop={286}
    title={t('next.states.gettingReady', {server: serverName})} body={t('next.states.gettingReadyBody')}
    detailsPresentation="link" detailsAction={onSwitchServer
      ? {id: 'switch-server', label: t('next.states.switchServer'), icon: 'i-server', onPress: onSwitchServer} : undefined}/>;
  return <StateView variant="centered" title={t('web.empty.homeTitle')}
    body={t(owner ? 'web.empty.homeOwnerBody' : 'web.empty.homeMemberBody')}/>;
}
