import React, {type ComponentProps, type ReactNode} from 'react';
import {View} from 'react-native';
import {Select} from '../controls';
import {StateView} from '../states';
import type {NativeShellRuntime} from '../../shell/runtime';

/** One of This device's setting rows with its own failure line under it, dismissed in place. */
export function DeviceRowBlock({error, onDismiss, runtime, children}: {error: unknown; onDismiss: () => void; runtime: NativeShellRuntime; children: ReactNode}) {
  return <View>
    {children}
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, onDismiss)}/>}
  </View>;
}

/** A choice drawn as a settings row's value at its end (tv-more 22/31: an ordinary row of the card). */
export function RowValueSelect(props: ComponentProps<typeof Select>) {
  return <Select {...props} style={{backgroundColor: 'transparent', paddingHorizontal: 0}}/>;
}
