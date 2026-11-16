import React from 'react';
import {TVServerReadOnlyPanel} from './TVServerReadOnlyPanel';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../lifetime';

/** Remote values/status use the same GET-only owner reader and fact rows as the other pages. */
export function TVRemoteAccessPanel(props: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}) {
  return <TVServerReadOnlyPanel {...props} page="remote"/>;
}
