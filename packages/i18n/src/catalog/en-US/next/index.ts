import {platformMessages} from './platform.ts';
import {controlsMessages} from './controls.ts';
import {overlaysMessages} from './overlays.ts';
import {artworkMessages} from './artwork.ts';
import {cardsMessages} from './cards.ts';
import {browseMessages} from './browse.ts';
import {layoutMessages} from './layout.ts';
import {statesMessages} from './states.ts';
import {shellMessages} from './shell.ts';
import {dockMessages} from './dock.ts';
import {castPlayerMessages} from './cast-player.ts';
import {mediaMessages} from './media.ts';
import {homeMessages} from './home.ts';
import {savedMessages} from './saved.ts';
import {liveMessages} from './live.ts';
import {controlsNativeMessages} from './controls-native.ts';
import {overlaysNativeMessages} from './overlays-native.ts';
import {artworkNativeMessages} from './artwork-native.ts';
import {cardsNativeMessages} from './cards-native.ts';
import {browseNativeMessages} from './browse-native.ts';
import {layoutNativeMessages} from './layout-native.ts';
import {statesNativeMessages} from './states-native.ts';
import {shellNativeMessages} from './shell-native.ts';
import {dockNativeMessages} from './dock-native.ts';
import {shared} from './shared.ts';
import {homeContractsMessages} from './home-contracts.ts';
import {selectionMessages} from './selection.ts';
import {selectionNativeMessages} from './selection-native.ts';
import {homeNativeMessages} from './home-native.ts';
import {ownerLibrariesMessages} from './owner-libraries.ts';
import {dashboardMessages} from './dashboard.ts';
import {settingsMessages} from './settings.ts';
import {settingsSpecMessages} from './settings-spec.ts';
import {readerMessages} from './reader.ts';
import {searchMessages} from './search.ts';
import {showMessages} from './show.ts';
import {remoteSourcesMessages} from './remote-sources.ts';
import {detailMessages} from './detail.ts';
import {infoMessages} from './info.ts';
import {musicMessages} from './music.ts';
import {deviceCapabilitiesMessages} from './device-capabilities.ts';
import {playerMessages} from './player.ts';

export const nextMessages = {
  ...platformMessages,
  ...controlsMessages,
  ...overlaysMessages,
  ...artworkMessages,
  ...cardsMessages,
  ...browseMessages,
  ...layoutMessages,
  ...statesMessages,
  ...shellMessages,
  ...dockMessages,
  ...castPlayerMessages,
  ...mediaMessages,
  ...homeMessages,
  ...savedMessages,
  ...liveMessages,
  ...controlsNativeMessages,
  ...overlaysNativeMessages,
  ...artworkNativeMessages,
  ...cardsNativeMessages,
  ...browseNativeMessages,
  ...layoutNativeMessages,
  ...statesNativeMessages,
  ...shellNativeMessages,
  ...dockNativeMessages,
  ...shared,
  ...homeContractsMessages,
  ...selectionMessages,
  ...selectionNativeMessages,
  ...homeNativeMessages,
  ...dashboardMessages,
  ...settingsMessages,
  ...settingsSpecMessages,
  ...readerMessages,
  'next.reading.savedOnlySettingsTitle': 'Reading settings',
  'next.reading.savedOnlySettingsBody': 'Reading settings aren’t available in this saved-only opening.',
  ...searchMessages,
  ...showMessages,
  ...remoteSourcesMessages,
  ...ownerLibrariesMessages,
  ...detailMessages,
  ...infoMessages,
  ...musicMessages,
  ...deviceCapabilitiesMessages,
  ...playerMessages,
} as const satisfies Record<`next.${string}`, string>;

export type NextMessageId = keyof typeof nextMessages;
