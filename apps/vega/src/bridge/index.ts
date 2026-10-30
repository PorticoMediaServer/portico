import React from 'react';
import {View, type ViewProps} from '@amazon-devices/react-native-kepler';
import {PorticoAccountAuth, PorticoActivity, PorticoCapabilities, PorticoCast, PorticoDownloads, PorticoImagePicker, PorticoSiriRemote, PorticoSubtitleFiles, PorticoTopShelf} from './modules';
import {PorticoNetwork} from './network';
import {PorticoPlaybackModule, PorticoPlayer} from './playback';
import {PorticoRuntime} from './runtime';
import {PorticoSystemMedia} from './system-media';
import {traced} from '../debug';
import {eventsOf, withEvents} from './events';

/**
 * Every module the shared `src/bridge` expects under `NativeModules`, by its Apple name. On Vega
 * they are JavaScript over the SDK's own APIs; the `react-native` shim serves them, so the shared
 * bindings find them exactly where they look on Apple.
 */
const modules: Record<string, object> = {
  PorticoRuntime,
  PorticoNetwork,
  PorticoCapabilities,
  PorticoPlaybackModule,
  PorticoSystemMedia,
  PorticoAccountAuth,
  PorticoActivity,
  PorticoSiriRemote,
  PorticoCast,
  PorticoDownloads,
  PorticoTopShelf,
  PorticoImagePicker,
  PorticoSubtitleFiles,
};
export const porticoModules: Readonly<Record<string, object>> = Object.freeze(Object.fromEntries(Object.entries(modules).map(([name, module]) => {
  const served = traced(name, module);
  // A traced module is a different object: its events are still the module's.
  const events = eventsOf(module);
  return [name, events && served !== module ? withEvents(served, events) : served];
})));

/** A native view with nothing to draw on a television: it takes no space and no focus. */
const Absent = (_props: ViewProps): null => null;
/** `PorticoBackdrop` is exported by the shared bridge and used by no screen. */
const Backdrop = (props: ViewProps): React.ReactElement => React.createElement(View, props);

/** The views the shared `NativeViews.ts` asks `requireNativeComponent` for. */
export const porticoViews: Readonly<Record<string, React.ComponentType<any>>> = Object.freeze({
  PorticoPlayer,
  PorticoBackdrop: Backdrop,
  PorticoAudioRoutePicker: Absent,
  PorticoVolumeView: Absent,
});

/** What `UIManager.hasViewManagerConfig` answers: only views that draw something. */
export const porticoViewConfigs: ReadonlySet<string> = new Set(['PorticoPlayer', 'PorticoBackdrop']);
