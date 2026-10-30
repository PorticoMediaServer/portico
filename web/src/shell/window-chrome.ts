import {useLayoutEffect, useRef, useSyncExternalStore} from 'react';
import {createWindowChromePublisher, getWindowChromeEnvironment, getWindowChromeRenderer, subscribeWindowChromeEnvironment, type WindowChromePublisher} from '../platform/window-chrome';

const subscribeFullscreen = (listener: () => void) => {
  document.addEventListener('fullscreenchange', listener);
  return () => document.removeEventListener('fullscreenchange', listener);
};
const domFullscreen = () => !!document.fullscreenElement;
const noFullscreen = () => false;
const noEnvironment = () => undefined;

/** Page state only. The actual video and music controls own their separate publishers. */
export function usePageWindowChrome(scrolled: boolean) {
  const environment = useSyncExternalStore(subscribeWindowChromeEnvironment, getWindowChromeEnvironment, noEnvironment);
  const renderer = useSyncExternalStore(subscribeWindowChromeEnvironment, getWindowChromeRenderer, noEnvironment);
  const fullscreen = useSyncExternalStore(subscribeFullscreen, domFullscreen, noFullscreen);
  const publisher = useRef<WindowChromePublisher | undefined>(undefined);
  useLayoutEffect(() => {
    const current = createWindowChromePublisher('pages');
    publisher.current = current;
    return () => { current.dispose(); if (publisher.current === current) publisher.current = undefined; };
  }, []);
  useLayoutEffect(() => { publisher.current?.update({controlsVisible: true, fullscreen, scrolled}); }, [fullscreen, scrolled]);
  return {renderer, os: environment?.os, fullscreen: fullscreen || environment?.fullscreen === true};
}
