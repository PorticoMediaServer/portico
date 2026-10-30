import React from 'react';
import {requireNativeComponent, type ViewProps} from 'react-native';
import {hasAirPlay} from '../device-platform';

// The installed iOS view owns the system picker. Do not instantiate its empty
// Android/tvOS counterpart, or replace it with a second playback transport.
const RoutePicker = hasAirPlay
  ? requireNativeComponent<ViewProps & {video: boolean}>('PorticoAudioRoutePicker')
  : undefined;

export function NativeAirPlayRoutePicker(props: ViewProps) {
  return RoutePicker ? <RoutePicker {...props} video/> : null;
}
