import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {BrowseFrame} from '../../../kit/frames';
import {Banner} from '../../../kit/overlays';
import {DownloadEntryList, LiveDownloadRow} from '../../../kit/saved/DownloadInventory';
import {OfflinePlayerPanel} from '../../../kit/saved/OfflinePlayerPanel';
import {nativeDownloadsCopy} from '../../../screens/downloads/presentation';
import type {RememberedDownloadsOpening} from '../../../shell/remembered-downloads-opening';
import {useRememberedDownloads, useRememberedPlayer, useRememberedShortcut, type RememberedRun} from '../../shared/downloads/useRememberedDownloads';

const t = defaultI18n.t, text = Object.freeze({text: Object.freeze({t: defaultI18n.t, locale: defaultI18n.locale})});

/** A phone's downloads while it can't reach its server (states 4a, "Your downloads still play"): Downloads' header with
 * Back, the player once something plays, then the downloads that play here as Downloads draws them, each with Play
 * or Read. A book or comic opens in the saved-only reader. */
export function RememberedDownloadsPage({source, onBack}: {source: RememberedDownloadsOpening; onBack: () => void}) {
  const view = useRememberedDownloads({source, onBack});
  if (view.reading && source.noticeCopy) {const ReadingScreen = (require('./RememberedReadingPage') as typeof import('./RememberedReadingPage')).RememberedReadingPage;
    return <ReadingScreen reading={view.reading} noticeCopy={source.noticeCopy} current={view.current} onBack={view.closeReading}/>;}
  return <BrowseFrame testID="remembered-downloads" header={{title: t('downloads.title'), back: {label: t('action.back'), onPress: view.back, testID: 'remembered-downloads-back'}}}
    grid={<DownloadEntryList ids={view.entries} empty={t('downloads.emptyTitle')} header={source.player || view.failure ? <>
      {source.player && <RememberedPlayer player={source.player} busy={view.busy} current={view.current} run={view.run}/>}
      {view.failure && <Banner tone="error" placement="inline" message={view.failure}/>}
    </> : undefined} renderRow={(id, first) => <LiveDownloadRow id={id} inventory={view.inventory} device={source.device} runtime={text}
      copy={nativeDownloadsCopy} separator={!first} menuOpen={false} onPlay={view.busy ? undefined : view.play}/>}/>}/>;
}

function RememberedPlayer(props: {player: NonNullable<RememberedDownloadsOpening['player']>; busy: boolean; current: () => boolean; run: RememberedRun}) {
  return <OfflinePlayerPanel {...useRememberedPlayer(props)}/>;
}

/** The same downloads from a recovery page that doesn't draw "Your downloads still play" itself. */
export function RememberedDownloadsShortcut({source}: {source: RememberedDownloadsOpening}) {
  const open = useRememberedShortcut(source);
  return open ? <Button variant="glass" onPress={open}>{t('downloads.title')}</Button> : null;
}
