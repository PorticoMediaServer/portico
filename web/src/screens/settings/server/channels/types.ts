import type {Library} from '@core/index.ts';
import type {ChannelApi} from '@core/channel-guide.ts';
import type {LibraryChannel} from '@core/library-channels.ts';
import type {ArtworkSource} from '../../../../kit/artwork/types';
import type {ChannelLogoUpload} from './channel-logo-source';

/** Supplied by the real primary-owner/manageServer Settings composition.
 * Neither credentials nor an alternative transport are owned by this page. */
export type LibraryChannelsPageProps=Readonly<{
  subscribe?:(listener:()=>void)=>()=>void;
  initialChannelId?:string;
  api:ChannelApi;
  serverId:string;
  isCurrent:()=>boolean;
  reportError:(error:unknown)=>void;
  /** Actual authorized bootstrap libraries; no inferred or example entries. */
  libraries:readonly Library[];
  /** The current device's real IANA zone, used only for a new draft. */
  timeZone:string;
  /** Existing authenticated artwork scheduler, fixed registered logo route.
   * A supplied revision came from a checked logo receipt, not the config. */
  sampleArt?:(itemId:string)=>ArtworkSource|undefined;
  logo:(channel:LibraryChannel,revision?:number)=>ArtworkSource|undefined;
  /** Fixed registered POST logo route, authenticated by the captured owner.
   * FormData fields: file and operationId; no config revision is invented. */
  uploadLogo:ChannelLogoUpload;
}>;
