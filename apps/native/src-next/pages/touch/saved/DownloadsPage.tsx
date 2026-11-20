import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {BrowseFrame} from '../../../kit/frames';
import {Menu} from '../../../kit/overlays';
import {DownloadEntryMenu, DownloadInventoryWindow, DownloadStorage, DownloadsEmpty, DownloadsInventory,
  DownloadsRules, DownloadsScroll, DownloadsTransfers, OtherProfileDownloads} from '../../../kit/saved/DownloadInventory';
import {StateView} from '../../../kit/states';
import {downloadEntryIdentity} from '../../../screens/downloads/presentation';
import {SavedConfirmSheet} from '../../../screens/saved/SavedForms';
import {limits, useDownloads, type DownloadsPageProps} from '../../shared/saved/useDownloads';

const t = defaultI18n.t;

export function DownloadsPage(props: DownloadsPageProps) {
  const {runtime, device, back, inline} = props;
  const state = useDownloads(props);
  const {snapshot, copy, inventory, membership, viewport, scroll, limitRef, inventoryTop, setInventoryTop, setPaneWidth, paneWidth,
    empty, storage, requests, waiting, cancelWaiting, expanded, readingTablet, readingColumn, rulesBeside, heldBytes,
    readingBytes, watchingBytes, readingBarWatching, readingBarReading, readingBarGap,
    failure, setEntryMenu, entryMenu, menu, menuItems, sheetMenu, afterMenu, pickingLimit, setPickingLimit,
    pending, removingOthers, closeRemoveOthers, openRemoveOthers, confirmRemoveOthers,
    removing, setRemoving, removeEntry, openMenu, playEntry, retryEntry, save, retry} = state;
  const overlays = <>
    <DownloadEntryMenu sheetMenu={sheetMenu} menu={menu} menuItems={menuItems} anchor={entryMenu?.anchor}
      close={() => setEntryMenu(undefined)} afterMenu={afterMenu} copy={copy}/>
    <Menu testID="downloads-storage-limit" open={pickingLimit} onOpenChange={setPickingLimit} anchorRef={limitRef} returnFocusRef={limitRef}
      title={t('downloads.storageLimit')} items={limits.map(value => ({id: String(value),
        label: value ? defaultI18n.bytes(value) : t('downloads.noLimit'), selected: snapshot.settings.limitBytes === value,
        onSelect: () => save({limitBytes: value})}))} presentation={sheetMenu ? 'sheet' : 'anchored'}/>
    <SavedConfirmSheet open={!!removingOthers} title={t('confirm.removeOthers.title')} action={t('action.remove')} destructive busy={pending}
      body={removingOthers ? `${t('downloads.othersOnDevice', {size: defaultI18n.bytes(removingOthers.bytes)})} ${t('confirm.removeOthers.body')}` : ''}
      testID="downloads-remove-other-profiles-confirm" onClose={closeRemoveOthers} onConfirm={confirmRemoveOthers}/>
    <SavedConfirmSheet open={!!removing} title={t('confirm.removeDownload.title')} action={t(copy.removeFromPhone)} destructive busy={pending}
      body={removing?.title ? t('confirm.removeDownload.body', {title: removing.title}) : t('confirm.removeDownload.bodyUntitled')}
      testID="downloads-remove-confirm" onClose={() => setRemoving(undefined)} onConfirm={removeEntry}/>
  </>;
  const body = <DownloadsScroll node={scroll} inline={inline} onWidth={setPaneWidth} viewport={viewport} overlays={overlays}>
    {!snapshot.supported ? <StateView title={t('downloads.unavailableOnDevice')}/> : <>
      {failure && <StateView {...runtime.error(failure, retry)}/>}
      {snapshot.error && <StateView title={snapshot.error} tone="error"/>}
      {empty && <DownloadsEmpty copy={copy}/>}
      {!empty && snapshot.ready && <DownloadStorage snapshot={snapshot} copy={copy} expanded={expanded} readingTablet={readingTablet}
        storage={storage} heldBytes={heldBytes} readingBytes={readingBytes} watchingBytes={watchingBytes} readingBarGap={readingBarGap}
        readingBarWatching={readingBarWatching} readingBarReading={readingBarReading}/>}
      <DownloadsTransfers requests={requests} waiting={waiting} pending={pending} cancel={cancelWaiting}/>
      <DownloadsInventory beside={rulesBeside} expanded={expanded && !empty} onTop={setInventoryTop} rules={<>
        <DownloadsRules settings={snapshot.settings} pending={pending} copy={copy} save={save} limitRef={limitRef} onLimit={() => setPickingLimit(true)}/>
        {snapshot.otherBytes > 0 && <OtherProfileDownloads size={snapshot.otherBytes} copy={copy} pending={pending} openRemoveOthers={openRemoveOthers}/>}
      </>}>
        {!empty && <DownloadInventoryWindow inventory={inventory} membership={membership} viewport={viewport} top={inventoryTop}
          expanded={expanded} readingTablet={readingTablet} readingColumn={readingColumn} rulesBeside={rulesBeside} paneWidth={paneWidth}
          device={device} runtime={runtime} copy={copy} menuId={menu ? downloadEntryIdentity(menu) : undefined}
          onMenu={openMenu} onPlay={device.openOffline ? playEntry : undefined} onRetry={retryEntry}/>}
      </DownloadsInventory>
    </>}
  </DownloadsScroll>;
  return inline ? body : <BrowseFrame header={{title: t('downloads.title'), back: {label: t('saved.title'), onPress: back, testID: 'downloads-back'}}} grid={body}/>;
}
