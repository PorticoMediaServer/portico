import {useSyncExternalStore} from 'react';
export {useWindowWidth as useShellWidth} from '../kit/theme';
const subscribeScroll = (listener: () => void) => {
  window.addEventListener('scroll', listener, {passive: true});
  return () => window.removeEventListener('scroll', listener);
};
export function useShellScrolled(): boolean { return useSyncExternalStore(subscribeScroll, () => window.scrollY > 16, () => false); }
