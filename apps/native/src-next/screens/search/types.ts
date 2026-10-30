import type {ReactNode, RefObject} from 'react';
import type {View} from 'react-native';
import type {SearchEntry, SearchGroup, SearchGroupResult, SearchSnapshot} from '../../../../../packages/client-core/src/search';
import type {MessageId} from '../../../../../packages/i18n/src';
import type {CardSelection} from '../../kit/cards';
import type {NativeEntryMenuState} from '../destinations/entry-actions';

/** These are bounded, authorized producer tiles, never inferred from loaded results. */
export type NativeSearchGenre = Readonly<{
  id: string; name: string; count: number; backdropPath?: string;
  coverPaths: readonly string[];
}>;
export type NativeSearchExploreSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  genres: readonly NativeSearchGenre[];
  /** Independent server-ordered phone-empty slot; never derived from covers. */
  backdropGenres?: readonly NativeSearchGenre[];
  hasGenres?: boolean;
  initialHeading: MessageId; emptyHeading: MessageId;
  error?: unknown;
}>;
export type NativeSearchExplorePort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeSearchExploreSnapshot;
  load: () => Promise<void>;
  openGenre: (genre: NativeSearchGenre) => void;
}>;
export type NativeSearchSelectionPort = Readonly<{
  /** The producer's exact scope/query/revision and IDs, with bounded mounted windows. */
  register: (snapshot: SearchSnapshot, groups: readonly SearchGroupResult[]) => () => void;
  item: (entry: SearchEntry, group: SearchGroup) => CardSelection | undefined;
  /** The screen's own artwork resolver, for the destination picker's rows. */
  render: (artwork: (path?: string) => ReactNode) => ReactNode;
}>;
export type NativeSearchActions = Readonly<{
  menuState?: NativeEntryMenuState;
  /** Non-media rows have no generic media-menu grant. */
  canMenu?: (entry: SearchEntry, group: SearchGroup) => boolean;
  open: (entry: SearchEntry, group: SearchGroup) => void;
  play: (entry: SearchEntry, group: SearchGroup) => void;
  menu: (entry: SearchEntry, group: SearchGroup, anchorRef?: RefObject<View | null>) => void;
  isMenuOpen?: (entry: SearchEntry, group: SearchGroup) => boolean;
}>;
export type NativeSearchTVCopy = Readonly<{preferToTalk: MessageId; voiceHint: MessageId}>;
