import {useLayoutEffect, useRef, useState, type ImgHTMLAttributes, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {fileInfoModel, type FileFacts} from '@core/presentation/kit-model';
import {overlaysFixtures} from '../../../../packages/i18n/src/catalog/en-US/next/dev/overlays-fixtures';
import {defineGalleryFamily} from '../gallery-registry';
import {Button, Segmented} from '../../kit/controls';
import {Banner, BottomSheet, Dialog, FileInfo, Menu, MenuGroup, MenuItem, MenuRow, MenuSeparator, Popover, SidePanel, Toast, ToastHost, Tooltip, type OverlayDismissReason, type ToastData} from '../../kit/overlays';
import styles from '../../kit/overlays/gallery.module.css';

const t = defaultI18n.t;
// Reference-only data; this module is discovered exclusively by Vite's serve gallery.
const reference = {server: overlaysFixtures['next.overlays.dev.server'], film: overlaysFixtures['next.overlays.dev.film'], removedFilm: overlaysFixtures['next.overlays.dev.removedFilm'], programme: overlaysFixtures['next.overlays.dev.programme'], year: 2024};
const files: readonly FileFacts[] = [
  {id: 'version-1', available: true, width: 3840, height: 1608, videoCodec: 'hevc', dynamicRange: 'hdr10', bitDepth: 10, bitRate: 52_000_000, container: 'mkv', sizeBytes: 64.2 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'truehd', channels: 8, objectAudio: 'atmos'}, {id: 'a2', language: 'en', codec: 'ac3', channels: 6}, {id: 'a3', language: 'fr', codec: 'eac3', channels: 8}, {id: 'a4', language: 'es', codec: 'eac3', channels: 8}],
    subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'en', hearingImpaired: true}, {id: 's3', language: 'fr'}, {id: 's4', language: 'es'}]},
  {id: 'version-2', available: true, width: 1920, height: 804, videoCodec: 'h264', bitRate: 12_000_000, container: 'mkv', sizeBytes: 14.6 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'eac3', channels: 6}, {id: 'a2', language: 'en', codec: 'aac', channels: 2}], subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'es'}]},
];
// Phone reference uses different files, supplied codec profiles and a supplied picked ID.
// These explicit drawn DEV facts never imply a profile or selection for production files.
const phonePickedFileId = 'version-1';
const phoneFiles: readonly FileFacts[] = [
  {id: 'version-1', available: true, width: 3840, height: 2160, videoCodec: 'hevc', videoProfile: 'Main 10', dynamicRange: 'dolby_vision', bitDepth: 10, bitRate: 53_000_000, container: 'mkv', sizeBytes: 61.4 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'truehd', channels: 8}, {id: 'a2', language: 'en', codec: 'ac3', channels: 6}], subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'en', hearingImpaired: true}, {id: 's3', language: 'fr'}, {id: 's4', language: 'es'}]},
  {id: 'version-2', available: true, width: 1920, height: 1080, videoCodec: 'h264', videoProfile: 'High', bitRate: 12_000_000, container: 'mp4', sizeBytes: 14.2 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'aac', channels: 6}], subtitles: []},
];
function useFixture() {
  const [open, setOpen] = useState(true), [event, setCurrentEvent] = useState(''), events = useRef<string[]>([]);
  const setEvent = (value: string) => { events.current.push(value); setCurrentEvent(value); };
  return {open, event, events: events.current, setEvent, change: (value: boolean, reason: OverlayDismissReason) => { setOpen(value); setEvent(reason); }, show: () => setOpen(true)};
}
function Fixture({children, event, events}: {children: ReactNode; event?: string; events?: string[]}) { return <div className={styles.canvas} data-overlay-event={event} data-overlay-events={JSON.stringify(events)}>{children}</div>; }

function MenuCase({states = false}: {states?: boolean}) {
  const fixture = useFixture(), [favorite, setFavorite] = useState(false), fileFocus = useRef<HTMLButtonElement | null>(null), trigger = useRef<HTMLButtonElement | null>(null);
  return <Fixture event={fixture.event} events={fixture.events}><div className={styles.anchor}><Menu open={fixture.open} onOpenChange={fixture.change} title={reference.film} returnFocusRef={trigger} initialFocusRef={fileFocus}
    trigger={<Button ref={trigger} variant="glass" size="compact" iconOnly icon="i-more" aria-label={t('next.overlays.more')} />} placement="bottom-start">
    <MenuGroup><MenuItem icon="i-heart" selected={states ? favorite : undefined} onSelect={() => setFavorite(!favorite)}>{t(favorite ? 'next.overlays.removeFavorite' : 'next.overlays.favorite')}</MenuItem>
      <MenuItem icon="i-star" onSelect={() => fixture.setEvent('rate')}>{t('next.overlays.rate')}</MenuItem>
      <MenuItem icon="i-listadd" disabled={states} onSelect={() => fixture.setEvent('playlist')}>{t('next.overlays.playlist')}</MenuItem>
      <MenuItem icon="i-layers" onSelect={() => fixture.setEvent('collection')}>{t('next.overlays.collection')}</MenuItem>
      <MenuItem icon="i-download" onSelect={() => fixture.setEvent('download')}>{t('next.overlays.download')}</MenuItem></MenuGroup>
    <MenuSeparator /><MenuGroup><MenuItem icon="i-film" onSelect={() => fixture.setEvent('version')}>{t('next.overlays.differentVersion')}</MenuItem><MenuItem ref={fileFocus} icon="i-info" onSelect={() => fixture.setEvent('file-info')}>{t('next.overlays.fileInfo')}</MenuItem></MenuGroup>
    <MenuGroup label={t('next.overlays.owner')} ownerOnly><MenuItem icon="i-edit" onSelect={() => fixture.setEvent('edit')}>{t('next.overlays.editMetadata')}</MenuItem><MenuItem icon="i-refresh" onSelect={() => fixture.setEvent('refresh')}>{t('next.overlays.refreshMetadata')}</MenuItem><MenuItem icon="i-trash" destructive onSelect={() => fixture.setEvent('delete')}>{t('next.overlays.delete')}</MenuItem></MenuGroup>
  </Menu></div></Fixture>;
}
function DesktopMenu() { return <MenuCase />; }
function MenuStates() { return <MenuCase states />; }
function PhoneMenu() {
  const fixture = useFixture(), trigger = useRef<HTMLButtonElement | null>(null);
  return <Fixture event={fixture.event} events={fixture.events}><Menu open={fixture.open} onOpenChange={fixture.change} title={<>{reference.film}<small className={styles.menuYear}>{reference.year}</small></>} returnFocusRef={trigger} trigger={<Button ref={trigger} size="compact" onClick={fixture.show}>{t('next.overlays.more')}</Button>}>
    <MenuGroup><MenuItem icon="i-check" onSelect={() => fixture.setEvent('watched')}>{t('next.overlays.watched')}</MenuItem><MenuItem icon="i-heart" onSelect={() => fixture.setEvent('favorite')}>{t('next.overlays.favorite')}</MenuItem><MenuItem icon="i-star" onSelect={() => fixture.setEvent('rate')}>{t('next.overlays.ratePhone')}</MenuItem><MenuItem icon="i-list" onSelect={() => fixture.setEvent('playlist')}>{t('next.overlays.playlist')}</MenuItem></MenuGroup>
    <MenuGroup><MenuItem icon="i-film" className={styles.versionChoice} onSelect={() => fixture.setEvent('version')}>{t('next.overlays.differentVersion')}</MenuItem><MenuItem icon="i-info" onSelect={() => fixture.setEvent('file-info')}>{t('next.overlays.fileInfo')}</MenuItem></MenuGroup>
    <MenuGroup label={t('next.overlays.ownerOnly')} ownerOnly><MenuItem icon="i-edit" onSelect={() => fixture.setEvent('edit')}>{t('next.overlays.editMetadataPhone')}</MenuItem><MenuItem icon="i-reload" onSelect={() => fixture.setEvent('refresh')}>{t('next.overlays.refreshMetadata')}</MenuItem></MenuGroup>
  </Menu></Fixture>;
}
function LongMenu() {
  const fixture = useFixture();
  return <Fixture event={fixture.event} events={fixture.events}><div className={styles.anchor}><Menu open={fixture.open} onOpenChange={fixture.change} title={reference.film} trigger={<Button size="small">{t('next.overlays.more')}</Button>} placement="bottom-start"><MenuItem selected onSelect={() => fixture.setEvent('long-action')}>{overlaysFixtures['next.overlays.dev.longTitle']}</MenuItem><MenuItem disabled onSelect={() => fixture.setEvent('disabled')}>{t('next.overlays.download')}</MenuItem></Menu></div></Fixture>;
}
function PopoverCase({modal = false}: {modal?: boolean}) {
  const fixture = useFixture(), [field, setField] = useState('year'), [direction, setDirection] = useState('desc'), focus = useRef<HTMLButtonElement | null>(null);
  return <Fixture event={fixture.event} events={fixture.events}><div className={styles.anchor}><Popover open={fixture.open} onOpenChange={fixture.change} title={t('next.overlays.sortBy')} modal={modal} initialFocusRef={focus} placement="bottom-start" trigger={<Button size="compact">{t('next.overlays.sortBy')}</Button>}>
    <div className={styles.sortHeading}>{t('next.overlays.sortBy')}</div><div className={styles.sortRows}>{(['forYou', 'title', 'dateAdded', 'year', 'releaseDate', 'duration', 'audienceRating', 'criticRating', 'yourRating', 'lastPlayed', 'playCount', 'resolution', 'random'] as const).map(value => <MenuRow key={value} ref={value === 'releaseDate' ? focus : undefined} description={value === 'forYou' ? t('next.overlays.mayTakeMoment') : undefined} selected={field === value} onClick={() => setField(value)}>{t(`next.overlays.${value}`)}</MenuRow>)}</div>
    <MenuSeparator /><div className={styles.sortHeading}>{t('next.overlays.order')}</div><Segmented className={styles.sortOrder} value={direction} onValueChange={setDirection} items={[{value: 'desc', label: t('next.overlays.newest')}, {value: 'asc', label: t('next.overlays.oldest')}]} aria-label={t('next.overlays.order')} />
  </Popover></div><Button className={styles.output} size="small" onClick={() => fixture.setEvent('outside-action')}>{t('next.overlays.done')}</Button></Fixture>;
}
function NonModalPopover() { return <PopoverCase />; }
function ModalPopover() { return <PopoverCase modal />; }
function DialogCase() {
  const fixture = useFixture(), cancel = useRef<HTMLButtonElement | null>(null);
  return <Fixture event={fixture.event} events={fixture.events}><Button size="compact" onClick={fixture.show}>{t('next.overlays.clearHistory')}</Button><Dialog open={fixture.open} onOpenChange={fixture.change} title={t('next.overlays.clearHistoryTitle')} description={t('next.overlays.clearHistoryBody')} className={styles.historyDialog} initialFocusRef={cancel} showCloseButton={false}
    footer={<><Button ref={cancel} size="small" onClick={() => fixture.change(false, 'action')}>{t('next.overlays.cancel')}</Button><Button variant="primary" size="small" onClick={() => fixture.change(false, 'action')}>{t('next.overlays.clearHistory')}</Button></>}>{null}</Dialog></Fixture>;
}
function RecordingDialog({responsive = false}: {responsive?: boolean} = {}) {
  const fixture = useFixture(), cancel = useRef<HTMLButtonElement | null>(null);
  return <Fixture event={fixture.event} events={fixture.events}><Button size="small" data-testid="open-recording" onClick={fixture.show}>{t('next.overlays.deleteRecording')}</Button><div className={styles.confirmBackplate}><ReferenceImage src="http://100.120.50.24:8787/assets/pool/movies/the-dark-knight--bd.jpg" className={styles.referenceBackdrop} /></div><Dialog responsive={responsive} className={responsive ? styles.phoneRecordingSheet : styles.recordingDialog} bodyClassName={responsive ? styles.phoneRecordingBody : undefined} headerClassName={responsive ? styles.phoneRecordingHeader : undefined} showCloseButton={!responsive} role="alertdialog" width={436} open={fixture.open} onOpenChange={fixture.change} title={t('next.overlays.deleteRecordingTitle')} initialFocusRef={cancel}
    footer={responsive ? undefined : <><Button ref={cancel} size="small" onClick={() => fixture.change(false, 'action')}>{t('next.overlays.cancel')}</Button><Button size="small" icon="i-trash" iconSize={18} className={styles.destructive} onClick={() => fixture.change(false, 'action')}>{t('next.overlays.deleteRecording')}</Button></>}><p><b>{reference.programme}</b>{t(responsive ? 'next.overlays.recordingRemovalPhone' : 'next.overlays.recordingRemoval', {server: reference.server})}</p>{responsive && <div className={styles.phoneRecordingActions}><Button size="small" className={styles.destructive} onClick={() => fixture.change(false, 'action')}>{t('next.overlays.deleteRecording')}</Button><Button ref={cancel} size="small" onClick={() => fixture.change(false, 'action')}>{t('next.overlays.cancel')}</Button></div>}</Dialog></Fixture>;
}
function PhoneRecordingDialog() { return <RecordingDialog responsive />; }
function PanelCase({owner = false}: {owner?: boolean}) {
  const fixture = useFixture(), model = fileInfoModel(files, owner ? {audience: 'owner', ownerFacts: {locations: files.map((file, index) => ({id: file.id, path: overlaysFixtures[index === 0 ? 'next.overlays.dev.desktopPathOne' : 'next.overlays.dev.desktopPathTwo'], canDelete: true})), canSplit: true, canMerge: false}, manageSurface: 'desktop'} : {audience: 'viewer'}, {t});
  return <Fixture event={fixture.event} events={fixture.events}><nav className={styles.toolbar}><Button size="small" onClick={() => fixture.setEvent('navigation')}>{t('next.overlays.done')}</Button><Button size="small" onClick={fixture.show}>{t('next.overlays.fileInfo')}</Button></nav>
    <FileInfo open={fixture.open} onOpenChange={fixture.change} model={model} subject={reference.film} layout="desktop" onManage={({fileId, action}) => fixture.setEvent(fileId ? `${fileId}:${action}` : action)} testID="file-info-panel" /></Fixture>;
}
function Panel() { return <PanelCase />; }
function OwnerPanel() { return <PanelCase owner />; }
/** A windowed read: two of five versions resident, the second still reading its audio. */
function WindowedPanel() {
  const fixture = useFixture(), resident = files.slice(0, 2).map((file, index) => ({...file, ordinal: index + 1, ...(index === 1 ? {audio: []} : {})}));
  const model = fileInfoModel(resident, {audience: 'viewer'}, {t}), more = {busy: false, failed: false, onPress: () => fixture.setEvent('more-versions')};
  return <Fixture event={fixture.event} events={fixture.events}><nav className={styles.toolbar}><Button size="small" onClick={fixture.show}>{t('next.overlays.fileInfo')}</Button></nav>
    <FileInfo open={fixture.open} onOpenChange={fixture.change} model={model} subject={reference.film} layout="desktop" onManage={() => {}} testID="file-info-windowed"
      continuation={{versionCount: 5, more, tracks: (id, lane) => lane !== 'audio' ? undefined : id === resident[1]!.id ? {pending: true} : {pending: false, more: {busy: false, failed: false, onPress: () => fixture.setEvent('more-tracks')}}}} /></Fixture>;
}
function SheetCase({full = false}: {full?: boolean}) {
  const fixture = useFixture(), [detent, setDetent] = useState(full ? 1 : 0), [scrollBody, setScrollBody] = useState<HTMLDivElement | null>(null);
  const model = fileInfoModel(phoneFiles, full ? {audience: 'owner', ownerFacts: {locations: phoneFiles.map((file, index) => ({id: file.id, path: overlaysFixtures[index === 0 ? 'next.overlays.dev.phonePathOne' : 'next.overlays.dev.phonePathTwo'], canDelete: true})), canSplit: true, canMerge: true}, manageSurface: 'phone'} : {audience: 'viewer'}, {t}, 'phone');
  useLayoutEffect(() => { if (full && scrollBody && fixture.open) scrollBody.scrollTop = 296; }, [full, scrollBody, fixture.open]);
  return <Fixture event={fixture.event} events={fixture.events}><Button size="compact" onClick={fixture.show}>{t('next.overlays.fileInfo')}</Button><FileInfo open={fixture.open} onOpenChange={fixture.change} model={model} subject={reference.film} layout="phone" pickedFileId={phonePickedFileId} onManage={({fileId, action}) => fixture.setEvent(fileId ? `${fileId}:${action}` : action)} detent={detent} onDetentChange={setDetent} scrollRef={setScrollBody} testID="file-info-sheet" /></Fixture>;
}
function HalfSheet() { return <SheetCase />; }
function FullSheet() { return <SheetCase full />; }
function ReferenceImage({alt = '', ...props}: ImgHTMLAttributes<HTMLImageElement>) {
  const [status, setStatus] = useState('loading');
  return <img {...props} alt={alt} data-reference-artwork={status}
    onLoad={event => { void event.currentTarget.decode().then(() => setStatus('decoded'), () => setStatus('failed')); }} onError={() => setStatus('failed')} />;
}
function ReferencePoster() { return <ReferenceImage src="http://100.120.50.24:8787/assets/pool/movies/titanic.jpg" />; }
function ToastBackplate({children}: {children: ReactNode}) {
  return <div className={styles.toastBackplate}><div className={styles.referencePosters}>{['the-incredibles', 'spirited-away', 'wall-e'].map(value => <ReferenceImage key={value} src={`http://100.120.50.24:8787/assets/pool/movies/${value}.jpg`} />)}</div>{children}</div>;
}
function ToastCase({action = false, host = false}: {action?: boolean; host?: boolean}) {
  const fixture = useFixture();
  const message = action ? <>{t('next.overlays.removalPrefix')}<b>{reference.removedFilm}</b>{t('next.overlays.removalSuffix')}</> : <><b>{t('next.overlays.detailsCopied')}</b>{' '}{t('next.overlays.detailsSafe')}</>;
  const undo = action ? {label: t('next.overlays.undo'), icon: host ? undefined : 'i-undo' as const, onAction: () => fixture.setEvent('undo')} : undefined;
  const [value, setValue] = useState<ToastData | null>({id: 'reference', message, action: undo, artwork: action ? <ReferencePoster /> : undefined, icon: action ? undefined : 'i-check', tone: 'success'});
  const surface = <div className={styles.toastPosition}><Toast open={fixture.open} onOpenChange={fixture.change} message={message} action={undo} artwork={action ? <ReferencePoster /> : undefined} icon={action ? undefined : 'i-check'} tone="success" /></div>;
  return <Fixture event={fixture.event} events={fixture.events}>{host ? <><Button size="small" onClick={() => setValue({id: String(Date.now()), message, action: undo, artwork: action ? <ReferencePoster /> : undefined})}>{t('next.overlays.done')}</Button><ToastHost toast={value} onDismiss={(id, reason) => { fixture.setEvent(`${id}:${reason}`); setValue(null); }} /></> : action ? <ToastBackplate>{surface}</ToastBackplate> : surface}</Fixture>;
}
function PlainToast() { return <ToastCase />; }
function ActionToast() { return <ToastCase action />; }
function PhoneToast() { return <ToastCase action host />; }
function QuietBannerCase({phone = false}: {phone?: boolean}) { return <Fixture><div className={phone ? styles.phoneBannerBackplate : styles.bannerBackplate}><ReferenceImage src="http://100.120.50.24:8787/assets/bd-spirited.jpg" className={styles.bannerBackdrop} /><div className={styles.bannerPosition}><Banner variant="quiet">{t('next.overlays.unreachableStart')}<b>{reference.server}</b>{t('next.overlays.unreachableEnd')}</Banner></div></div></Fixture>; }
function QuietBanner() { return <QuietBannerCase />; }
function PhoneQuietBanner() { return <QuietBannerCase phone />; }
function Notice() { const [open, setOpen] = useState(true); return <Fixture><div className={styles.noticePosition}><Banner open={open} icon="i-update" action={{label: t('next.overlays.reload'), onAction: () => setOpen(false)}}><b>{t('next.overlays.updated', {server: reference.server})}</b>{' '}{t('next.overlays.reloadHelp')}</Banner></div></Fixture>; }

/** Local controlled effects only: no API, storage, permissions or successful business operations. */
function TooltipCase({detailed = false}: {detailed?: boolean}) {
  const [open, setOpen] = useState(false);
  return <Fixture><div className={styles.anchoredTrigger}><Tooltip open={open} onOpenChange={setOpen} trigger={<Button size="small" data-testid="tooltip-trigger">{t('next.overlays.more')}</Button>} description={detailed ? overlaysFixtures['next.overlays.dev.tooltipTitle'] : undefined} testID="tooltip-content">{t(detailed ? 'next.overlays.previous' : 'next.overlays.pictureInPicture')}</Tooltip></div></Fixture>;
}
function DetailedTooltip() { return <TooltipCase detailed />; }
function RuntimeHarness() {
  const [surface, setSurface] = useState(''), [nested, setNested] = useState(false), [menu, setMenu] = useState(''), [tip, setTip] = useState(false);
  const [events, setEvents] = useState<string[]>([]), [result, setResult] = useState<'idle' | 'loading' | 'result' | 'error'>('idle'), [toast, setToast] = useState<ToastData | null>(null);
  const opener = useRef<HTMLButtonElement | null>(null), first = useRef<HTMLButtonElement | null>(null), anchored = useRef<HTMLButtonElement | null>(null);
  const deferred = useRef<((value: boolean) => void) | null>(null), count = useRef(0);
  const record = (value: string) => setEvents(previous => [...previous, value]);
  const change = (value: boolean, reason: OverlayDismissReason) => { record(`${surface}:${value}:${reason}`); if (!value) setSurface(''); };
  const notify = (action: boolean) => { count.current += 1; setToast({id: String(count.current), message: t('next.overlays.detailsCopied'), action: action ? {label: t('next.overlays.undo'), onAction: () => record('toast:undo')} : undefined}); };
  const submit = () => { setResult('loading'); record('submit'); void new Promise<boolean>(resolve => { deferred.current = resolve; }).then(value => { setResult(value ? 'result' : 'error'); record(value ? 'resolved' : 'rejected'); }); };
  const items = <><MenuItem onSelect={() => record('menu:first')}>{t('next.overlays.favorite')}</MenuItem><MenuItem disabled onSelect={() => record('menu:disabled')}>{t('next.overlays.playlist')}</MenuItem><MenuItem onSelect={event => { event.preventDefault(); record('menu:retained'); }}>{t('next.overlays.fileInfo')}</MenuItem><MenuItem destructive onSelect={() => record('menu:delete')}>{t('next.overlays.delete')}</MenuItem></>;
  return <Fixture><div data-testid="runtime-harness" data-events={JSON.stringify(events)} data-result={result} className={styles.harnessControls}>
    <Button ref={opener} size="small" data-testid="open-dialog" onClick={event => { opener.current = event.currentTarget; setSurface('dialog'); }}>{t('next.overlays.clearHistory')}</Button>
    <Button size="small" data-testid="open-side" onClick={event => { opener.current = event.currentTarget; setSurface('side'); }}>{t('next.overlays.editMetadata')}</Button>
    <Button size="small" data-testid="open-locked" onClick={event => { opener.current = event.currentTarget; setSurface('locked'); }}>{t('next.overlays.done')}</Button>
    <Button size="small" data-testid="open-sheet" onClick={event => { opener.current = event.currentTarget; setSurface('sheet'); }}>{t('next.overlays.fileInfo')}</Button>
    <Button size="small" data-testid="open-panel" onClick={event => { opener.current = event.currentTarget; setSurface('panel'); }}>{t('next.overlays.more')}</Button>
    <Button size="small" data-testid="outside" onClick={() => record('outside')}>{t('next.overlays.cancel')}</Button>
    <Menu open={menu === 'one'} onOpenChange={(value, reason) => { setMenu(value ? 'one' : ''); record(`one:${reason}`); }} title={t('next.overlays.more')} trigger={<Button size="small" data-testid="menu-one">{t('next.overlays.more')}</Button>}>{items}</Menu>
    <Menu open={menu === 'two'} onOpenChange={(value, reason) => { setMenu(value ? 'two' : ''); record(`two:${reason}`); }} title={t('next.overlays.sortBy')} trigger={<Button size="small" data-testid="menu-two">{t('next.overlays.sortBy')}</Button>}>{items}</Menu>
    <Button size="small" data-testid="toast-plain" onClick={() => notify(false)}>{t('next.overlays.detailsCopied')}</Button><Button size="small" data-testid="toast-action" onClick={() => notify(true)}>{t('next.overlays.undo')}</Button>
    <Button size="small" data-testid="resolve" onClick={() => deferred.current?.(true)}>{t('next.overlays.done')}</Button><Button size="small" data-testid="reject" onClick={() => deferred.current?.(false)}>{t('next.overlays.cancel')}</Button>
  </div>
    <Dialog open={['dialog', 'side', 'locked'].includes(surface)} onOpenChange={change} title={t('next.overlays.clearHistoryTitle')} description={t('next.overlays.clearHistoryBody')} placement={surface === 'side' ? 'right' : 'center'} responsive={false} initialFocusRef={first} returnFocusRef={opener} dismissOnBack={surface !== 'locked'} dismissOnOutsidePress={surface !== 'locked'} showCloseButton={surface !== 'locked'} testID="harness-dialog"
      footer={<><Button ref={first} size="small" data-testid="cancel-dialog" onClick={() => change(false, 'action')}>{t('next.overlays.cancel')}</Button><Button variant="primary" size="small" data-testid="submit" loading={result === 'loading'} onClick={submit}>{t('next.overlays.clearHistory')}</Button></>}>
      <div className={styles.harnessBody} data-testid="async-state" data-state={result}><span>{overlaysFixtures[`next.overlays.dev.${result}` as const]}</span><Button size="small" data-testid="callback-resolve" onClick={() => deferred.current?.(true)}>{overlaysFixtures['next.overlays.dev.resolve']}</Button><Button size="small" data-testid="callback-reject" onClick={() => deferred.current?.(false)}>{overlaysFixtures['next.overlays.dev.reject']}</Button><Tooltip open={tip} onOpenChange={setTip} trigger={<Button size="small" data-testid="nested-tooltip">{t('next.overlays.more')}</Button>} testID="nested-tooltip-content">{t('next.overlays.pictureInPicture')}</Tooltip><Menu open={nested} onOpenChange={(value, reason) => { setNested(value); record(`nested:${reason}`); }} title={t('next.overlays.more')} trigger={<Button size="small" data-testid="nested-menu">{t('next.overlays.more')}</Button>}>{items}</Menu></div>
    </Dialog>
    <BottomSheet open={surface === 'sheet'} onOpenChange={change} title={t('next.overlays.fileInfo')} detents={[422,740]} returnFocusRef={opener} testID="harness-sheet" footer={<Button size="small" data-testid="sheet-footer" onClick={() => change(false, 'action')}>{t('next.overlays.done')}</Button>}><div className={styles.harnessBody}>{Array.from({length: 20}, (_, index) => <Button size="small" key={index} onClick={() => record(`row:${index}`)}>{t('next.overlays.fileInfo')}</Button>)}</div></BottomSheet>
    <SidePanel open={surface === 'panel'} onOpenChange={change} title={t('next.overlays.fileInfo')} returnFocusRef={opener} closeOnOutsidePress testID="harness-panel"><Button size="small" onClick={() => record('panel:action')}>{t('next.overlays.done')}</Button></SidePanel>
    <Button ref={anchored} className={styles.anchoredTrigger} size="small" data-testid="anchored-menu" onClick={() => setMenu(menu === 'anchor' ? '' : 'anchor')}>{t('next.overlays.more')}</Button>
    <Menu presentation="anchored" placement="top-start" width={250} sideOffset={8} anchorRef={anchored} open={menu === 'anchor'} onOpenChange={(value, reason) => { setMenu(value ? 'anchor' : ''); record(`anchor:${reason}`); }} title={t('next.overlays.more')} testID="anchored-content">{items}</Menu>
    <ToastHost toast={toast} onDismiss={(id, reason) => { record(`toast:${id}:${reason}`); setToast(null); }} testID="harness-toast" />
  </Fixture>;
}

export default defineGalleryFamily({id: 'overlays', labelKey: 'next.overlays.gallery', cases: [
  {id: 'menu', labelKey: 'next.overlays.gallery.menu', width: 1440, height: 1214, render: DesktopMenu},
  {id: 'menu-long', labelKey: 'next.overlays.gallery.menuStates', width: 600, height: 480, render: LongMenu},
  {id: 'phone-dialog', labelKey: 'next.overlays.deleteRecordingTitle', width: 390, height: 844, render: PhoneRecordingDialog},
  {id: 'menu-states', labelKey: 'next.overlays.gallery.menuStates', width: 1440, height: 1214, render: MenuStates},
  {id: 'popover', labelKey: 'next.overlays.gallery.popover', width: 1440, height: 990, render: NonModalPopover},
  {id: 'modal-popover', labelKey: 'next.overlays.gallery.modalPopover', width: 1440, height: 990, render: ModalPopover},
  {id: 'dialog', labelKey: 'next.overlays.gallery.dialog', width: 1440, height: 810, render: DialogCase},
  {id: 'recording-dialog', labelKey: 'next.overlays.deleteRecordingTitle', width: 1440, height: 900, render: RecordingDialog},
  {id: 'side-panel', labelKey: 'next.overlays.gallery.panel', width: 1440, height: 1260, render: Panel},
  {id: 'side-panel-owner', labelKey: 'next.overlays.gallery.panelOwner', width: 1440, height: 1260, render: OwnerPanel},
  {id: 'side-panel-windowed', labelKey: 'next.overlays.gallery.panelWindowed', width: 1440, height: 1260, render: WindowedPanel},
  {id: 'sheet-half', labelKey: 'next.overlays.gallery.sheetHalf', width: 390, height: 844, render: HalfSheet},
  {id: 'sheet-full', labelKey: 'next.overlays.gallery.sheetFull', width: 390, height: 844, render: FullSheet},
  {id: 'phone-menu', labelKey: 'next.overlays.gallery.phoneMenu', width: 390, height: 844, render: PhoneMenu},
  {id: 'toast', labelKey: 'next.overlays.gallery.toast', width: 1440, height: 810, render: PlainToast},
  {id: 'toast-action', labelKey: 'next.overlays.gallery.toastAction', width: 1440, height: 810, render: ActionToast},
  {id: 'phone-toast', labelKey: 'next.overlays.gallery.phoneToast', width: 390, height: 844, render: PhoneToast},
  {id: 'banner', labelKey: 'next.overlays.gallery.banner', width: 1440, height: 900, render: QuietBanner},
  {id: 'phone-banner', labelKey: 'next.overlays.gallery.banner', width: 390, height: 200, render: PhoneQuietBanner},
  {id: 'tooltip-detail', labelKey: 'next.overlays.previous', width: 600, height: 300, render: DetailedTooltip},
  {id: 'tooltip', labelKey: 'next.overlays.more', width: 600, height: 300, render: TooltipCase},
  {id: 'runtime', labelKey: 'next.overlays.gallery', width: 1440, height: 900, render: RuntimeHarness},
  {id: 'notice', labelKey: 'next.overlays.gallery.notice', width: 390, height: 300, render: Notice},
]});
