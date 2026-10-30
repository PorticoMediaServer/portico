import {useSyncExternalStore} from 'react';
import type {RefObject} from 'react';
import type {PlayOnController} from '../../platform/playback/play-on';
import {CastDock} from './CastDock';
import {CastPlayer, CastTracks, CastTakeover} from './CastPlayer';
import {PlayOnPanel} from './PlayOnPanel';
import type {CastPlayerTranslate, CastPlayerVisuals, CastPlayerProps} from './types';

/** Sibling controls only. The shell's existing MediaSurface/video remain their sole owner. */
export function CastPlayback({controller, visuals, phone, anchorRef, reportError, t, receivers, linkTv}: Pick<CastPlayerProps, 'receivers' | 'linkTv'> & Readonly<{
  t?: CastPlayerTranslate; controller: PlayOnController; visuals: CastPlayerVisuals; phone: boolean;
  anchorRef: RefObject<HTMLElement | null>; reportError: (error: unknown) => void;
}>) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const props = {t, controller, snapshot, visuals, phone, returnFocusRef: anchorRef, reportError, receivers, linkTv};
  return <><CastDock {...props} /><PlayOnPanel {...props} anchorRef={anchorRef} /><CastPlayer {...props} /><CastTracks {...props} /><CastTakeover {...props} /></>;
}
