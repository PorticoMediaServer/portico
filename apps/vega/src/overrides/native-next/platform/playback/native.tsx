import React from 'react';
import {createNativeShellPlaybackAdapter as sharedFactory, type NativePlaybackFactoryOptions} from '../../../../../../native/src-next/platform/playback/native';
import {VegaSurfaceComposition} from '../../../../bridge/surface-composition';
export * from '../../../../../../native/src-next/platform/playback/native';

/** Keep the original adapter with Vega's effective rate and surface composition input. */
export function createNativeShellPlaybackAdapter(options: NativePlaybackFactoryOptions) {
  const adapter = sharedFactory({...options, settings: () => ({...options.settings(), videoRate: 1, audiobookRate: 1})});
  const SharedSurface = adapter.Surface;
  // The shared host forwards this composition prop only on Android. Keep its actual host,
  // refs, bounds, fence and lease calculation, carrying only the original caller's intent.
  adapter.Surface = function VegaSurface(props: React.ComponentProps<typeof SharedSurface>) {
    return <VegaSurfaceComposition.Provider value={props.surfaceActive === true}><SharedSurface {...props}/></VegaSurfaceComposition.Provider>;
  };
  return adapter;
}
