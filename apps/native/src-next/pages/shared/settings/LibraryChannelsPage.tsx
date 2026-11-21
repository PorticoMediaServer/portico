import React from 'react';
import {Channels} from '../../../kit/settings/channels/LibraryChannelsContent';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useLibraryChannelGroup} from './useLibraryChannels';

export function LibraryChannelsPage({ports, runtime, initialChannelId}: {
  ports: Pick<NativeSettingsLivePorts, 'context' | 'requestId'>; runtime: NativeShellRuntime; initialChannelId?: string;
}) {
  const group = useLibraryChannelGroup(ports, runtime);
  return group ? <Channels group={group} runtime={runtime} initialChannelId={initialChannelId}/> : null;
}
