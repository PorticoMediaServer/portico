import {useSyncExternalStore} from 'react';
import {sizeClassForWidth, type SizeClass} from '@core/index.ts';

const subscribeWidth = (listener: () => void) => {
  window.addEventListener('resize', listener);
  return () => window.removeEventListener('resize', listener);
};
const readWidth = () => window.innerWidth;
const startupWidth = () => 0;

/** Live layout width; independent of pointer capability and device identity. */
export function useWindowWidth(): number {
  return useSyncExternalStore(subscribeWidth, readWidth, startupWidth);
}
const readHeight = () => window.innerHeight;
/** Live layout height, for the short-side rule a turned phone needs. */
export function useWindowHeight(): number {
  return useSyncExternalStore(subscribeWidth, readHeight, startupWidth);
}
export function useSizeClass(): SizeClass {
  return sizeClassForWidth(useWindowWidth());
}

const coarseQuery = '(pointer: coarse)';
const subscribePointer = (listener: () => void) => {
  const media = window.matchMedia(coarseQuery);
  media.addEventListener('change', listener);
  return () => media.removeEventListener('change', listener);
};
const readCoarsePointer = () => window.matchMedia(coarseQuery).matches;
const startupCoarsePointer = () => false;

/** Pointer changes never alter SizeClass; they select expanded navigation chrome. */
export function useCoarsePointer(): boolean {
  return useSyncExternalStore(subscribePointer, readCoarsePointer, startupCoarsePointer);
}
