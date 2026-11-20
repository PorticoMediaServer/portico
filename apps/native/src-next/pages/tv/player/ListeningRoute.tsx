import React, {useLayoutEffect, useRef} from 'react';
import {equalizerCopy} from '../../../kit/player/listening-presentation';
import {useTVPanelPresence} from '../../../kit/overlays/TVPanelMotion';
import type {NowPlayingScreenProps} from '../../shared/player/audio-player';
import {TVAudioPlayerPage} from './AudioPlayerPage';

/** This retained snapshot is paint only; the host's live adapters remain authoritative. */
export function TVListeningRoute({value, scope, onPresenceChange}: {value?: NowPlayingScreenProps; scope: string; onPresenceChange?: (present: boolean) => void}) {
  const presence = useTVPanelPresence(value, scope);
  const present = presence.value !== undefined, report = useRef(onPresenceChange);
  report.current = onPresenceChange;
  useLayoutEffect(() => {onPresenceChange?.(present);}, [onPresenceChange, present]);
  useLayoutEffect(() => () => report.current?.(false), []);
  return presence.value ? <TVAudioPlayerPage {...presence.value} equalizerCopy={equalizerCopy()} tvOpen={presence.open} onTVExited={presence.exited}/> : null;
}
