import {useEffect} from 'react';
import {Platform, TVEventControl} from 'react-native';
import {createTVMenuPolicy} from './tv-menu-policy';

const policy = createTVMenuPolicy(capture => {
  if (capture) TVEventControl.enableTVMenuKey();
  else TVEventControl.disableTVMenuKey();
});

/** A shared lease prevents an outgoing owner from releasing another owner's Menu. */
export function captureTVMenuKey(capture = true): () => void {
  return Platform.OS === 'ios' && Platform.isTV ? policy.acquire(capture) : () => {};
}

export function useTVMenuCapture(capture: boolean): void {
  useEffect(() => captureTVMenuKey(capture), [capture]);
}
