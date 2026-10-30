import type {WebPlaybackEnvironment} from './types.ts';

let installed: WebPlaybackEnvironment = Object.freeze({});
/** Install once at the platform entry before importing the web app. No window/global registry. */
export function installPlaybackEnvironment(environment: WebPlaybackEnvironment): () => void {
  const previous = installed, next = Object.freeze({...environment});
  installed = next;
  return () => { if (installed === next) installed = previous; };
}
export function playbackEnvironment(): WebPlaybackEnvironment { return installed; }
