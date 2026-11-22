import React from 'react';
import {TVPeopleList} from '../../../kit/settings/TVPeopleList';
import {StateView} from '../../../kit/states';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {useTVPeople} from '../../shared/settings/useTVPeople';

/** TV37: Server settings > People on TV, read only: accounts and waiting invitations. */
export function PeoplePage(props: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}) {
  const groups = useTVPeople(props);
  if (!groups) return <StateView title={props.runtime.text.t('status.loading')}/>;
  return <TVPeopleList groups={groups} failure={props.runtime.error}/>;
}
