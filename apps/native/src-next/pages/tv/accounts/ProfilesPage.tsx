import React, {type ComponentProps, type ReactNode} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ProfilePicker} from '../../../kit/artwork';
import {TVAccountCanvas} from '../../../kit/frames/TVAuthLayout';

export function TVWhosWatching({children, testID}: {children: React.ReactNode; testID: string}) {
  return <TVAccountCanvas testID={testID} shape="round">{children}</TVAccountCanvas>;
}

export function ProfilesPage({serverName, picker, status}: {
  serverName: string; picker: Omit<ComponentProps<typeof ProfilePicker>, 'title'>; status?: ReactNode;
}) {
  return <TVAccountCanvas testID="accounts-profile-switch" shape="round">
    <ProfilePicker {...picker} title={serverName
      ? defaultI18n.t('auth.whosWatchingOnServer', {server: serverName}) : defaultI18n.t('auth.whosWatching')}/>
    {status}
  </TVAccountCanvas>;
}
