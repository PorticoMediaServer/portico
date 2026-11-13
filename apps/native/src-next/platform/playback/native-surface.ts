import {requireNativeComponent, type HostComponent, type ViewProps} from 'react-native';

const views = new Map<string, HostComponent<ViewProps>>();
/** Online and offline owners borrow the same registration; React Native permits it only once. */
export function nativePlaybackView<Props extends ViewProps>(name: string): HostComponent<Props> {
  let view = views.get(name);
  if (!view) {view = requireNativeComponent<ViewProps>(name); views.set(name, view);}
  return view as HostComponent<Props>;
}
