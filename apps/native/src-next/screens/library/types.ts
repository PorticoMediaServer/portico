import type {ReactNode, RefObject} from 'react';
import type {View} from 'react-native';
import type {Library} from '../../../../../packages/client-core/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {BrowseLabels} from '../../kit/browse';
import type {CardSelection} from '../../kit/cards';
import type {LayoutSelection} from '../../kit/layout';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeEntryMenuState} from '../destinations/entry-actions';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import type {NativeSelection} from '../selection/native-selection';
import type {NativeLibraryDirectory} from './directory';
import type {NativeLibraryFirstScanPort} from './first-scan';
import type {NativeMarkAllRequest} from './mark-all';
import type {NativeLibraryDestination, NativeLibrarySnapshot, NativeLibraryViewDraft} from './model';

export type NativeLibraryActions = Readonly<{
  dispose?: () => void;
  menuState?: NativeEntryMenuState;
  open: (entry: ContentEntry) => void;
  play: (entry: ContentEntry) => void;
  menu: (entry: ContentEntry, anchorRef?: RefObject<View | null>, /** The real selection entry point the menu's Select row runs. */ select?: () => void) => void;
  isMenuOpen?: (entry: ContentEntry) => boolean;
  saveView: (destination: NativeLibraryDestination, draft: NativeLibraryViewDraft) => void;
  /** The host's count-aware bulk command: one durable job over the view the viewer sees. */
  markAll?: (request: NativeMarkAllRequest) => void;
}>;
export type NativeLibrarySelectionPort = Readonly<{
  /** Enter this library's actual current selection context without selecting an item. */
  start: () => void;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => LayoutSelection;
  register: (source: NativeLibrarySnapshot, entries: readonly ContentEntry[], first: number, last: number) => () => void;
  item: (entry: ContentEntry) => CardSelection;
  /** The picker's rows take the screen's own real artwork resolver; nothing is drawn without one. */
  render: (artwork?: (path?: string) => ReactNode) => ReactNode;
}>;
export type NativeLibraryScreenProps = Readonly<{
  destination: NativeLibraryDestination;
  runtime: NativeShellRuntime;
  activation: NativeDestinationActivationPort;
  libraries: readonly Library[];
  directory: NativeLibraryDirectory;
  labels: BrowseLabels;
  actions: NativeLibraryActions;
  selection?: NativeLibrarySelectionPort;
  /** The Discover tab's own multi-select over the shelves it draws. */
  shelfSelection?: NativeSelection;
  owner: boolean;
  /** The owner's first-scan check and way to server activity (states.html #15). Absent for members. */
  firstScan?: NativeLibraryFirstScanPort;
  /** Home's Mixes More opens this library's Discover and lands on the Mixes row. */
  mixAnchor?: boolean;
}>;
