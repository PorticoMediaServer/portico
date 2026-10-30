import React from 'react';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {StateLayout} from '../../kit/states';

/** connect#4c / TV equivalent: refusal has no connection retry or sign-in offer. */
export function DirectoryAccessRefused({serverName, busy, onOther, onRefresh}: Readonly<{
  serverName: string; busy: boolean; onOther: () => void; onRefresh: () => void;
}>) {
  const t = defaultI18n.t;
  return <StateLayout testID="accounts-server-refused" icon="i-userx" tone="error"
    title={t('web.chooser.refusedTitle', {server: serverName})} body={t('web.chooser.refusedBody')}
    // connect#12: Choose another server goes back to the list and holds the starting focus.
    primaryAction={{id: 'choose-another', label: t('web.chooser.otherServer'), disabled: busy, hasTVPreferredFocus: true, onPress: onOther}}
    secondaryAction={{id: 'refresh-servers', label: t('accounts.servers.refresh'), loading: busy, disabled: busy, onPress: onRefresh}}/>;
}
