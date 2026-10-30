import type {RefObject} from 'react';
import type {MessageId} from '@i18n';
export type CastPlayerTranslate = (id: MessageId, values?: Record<string, string | number>) => string;
import type {DockPresentation} from '../dock';
import type {PlayOnController, PlayOnLinkPort, PlayOnReceiversPort, PlayOnSnapshot} from '../../platform/playback/play-on';

/** Display artwork is resolved by the authority-owned shell, never sent as a media grant. */
export type CastPlayerVisuals = Partial<Pick<DockPresentation, 'identity' | 'artwork' | 'ambient' | 'palette'>> & Pick<DockPresentation, 'skipBackSeconds' | 'skipForwardSeconds'> & Readonly<{
  /** Only the actual receiver item's visuals may follow a queue advance. */
  itemId?: string;
  onMore?: () => void; onMyList?: () => void;
}>;
export type CastPlayerProps = Readonly<{
  t?: CastPlayerTranslate; controller: PlayOnController; snapshot: PlayOnSnapshot; visuals: CastPlayerVisuals; phone: boolean;
  returnFocusRef?: RefObject<HTMLElement | null>; reportError: (error: unknown) => void;
  /** Portico televisions from the server's directory, and the TV sign-in code flow. */
  receivers?: PlayOnReceiversPort; linkTv?: PlayOnLinkPort;
}>;
export type PlayOnPanelProps = Pick<CastPlayerProps, 'controller' | 'snapshot' | 'phone' | 'reportError' | 't' | 'receivers' | 'linkTv'> & Readonly<{anchorRef: RefObject<HTMLElement | null>}>;
