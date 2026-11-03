import {useCallback, useEffect, useRef} from 'react';
import {tvPlayPauseOwnerHook} from './tv-play-pause';

/** TV-FOCUS-REVIEW step 6: register this component's meaning for the remote's Play/Pause (platform/tv-play-pause). */
export const useTVPlayPauseOwner = tvPlayPauseOwnerHook({useCallback, useEffect, useRef});
