import React from 'react';
import {Platform} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StateLayout} from '../../kit/states/StateLayout';
import type {NegotiationRefusal} from '../../platform/negotiation';

/** O-159's named refusal, shown before sign-in: the server needs an update (pre-release, or no
 * API major in common because it is older), or this app does (the server only speaks newer). */
export function ServerNeedsUpdate({refusal, onBack, backLabel, testID = 'accounts-server-needs-update'}: {refusal: NegotiationRefusal; onBack: () => void; backLabel?: string; testID?: string}) {
  const t = defaultI18n.t, server = refusal.serverName ?? '';
  const back = {id: 'back', label: backLabel ?? t('action.back'), onPress: onBack};
  if (refusal.kind === 'app') return <StateLayout testID={testID} icon="i-update" tone="accent"
    title={t('next.states.native.updateApp')}
    body={t(Platform.OS === 'ios' ? 'next.states.native.updateIOSBody' : 'next.states.native.updateAndroidBody', {server})}
    secondaryAction={back}/>;
  return <StateLayout testID={testID} icon="i-update" tone="warning"
    title={t('next.states.native.updateServer')} body={t('next.states.native.updateServerAnyBody', {server})}
    secondaryAction={back}/>;
}
