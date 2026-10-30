import {Fragment, useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import type {FactPart, FileInfoModel, ManageAction} from '@core/presentation/kit-model';
import type {FileInfoDisc} from '@core/detail.ts';
import {durationText} from '@core/presentation/kit-model/text.ts';
import {defaultI18n} from '@i18n';
import {Button} from '../controls/Button';
import {OwnerTag, ServerMark} from '../shell/ServerIdentity';
import {BottomSheet, type BottomSheetProps} from './BottomSheet';
import {Dialog} from './Dialog';
import type {OverlaySurfaceProps} from './types';
import styles from './file-info.module.css';

export type FileInfoAction = Readonly<{fileId?: string; action: ManageAction['id']}>;
export type FileInfoMore = Readonly<{busy: boolean; failed: boolean; onPress: () => void}>;
/** A windowed read's continuation. Versions, tracks and owner files arrive window by window;
 * everything stays reachable, and nothing here invents a row or a count. */
export type FileInfoContinuation = Readonly<{
  /** Every version of the title, not only the resident ones. */
  versionCount: number;
  more?: FileInfoMore;
  /** Earlier versions were let go to keep the list bounded; this reads from the first again. */
  earlier?: FileInfoMore;
  /** Once per version, when its section first comes into view. */
  onVersionVisible?: (id: string) => void;
  tracks?: (id: string, lane: 'audio' | 'subtitles') => Readonly<{pending: boolean; more?: FileInfoMore}> | undefined;
  locationsMore?: (versionId: string) => FileInfoMore | undefined;
}>;
export type FileInfoProps = Pick<OverlaySurfaceProps, 'open' | 'onOpenChange' | 'initialFocusRef' | 'returnFocusRef' | 'testID'> & {
  model: FileInfoModel;
  subject: string;
  /** Use the matching canonical fileInfoModel context. This component never projects metadata. */
  layout: 'desktop' | 'phone';
  /** An actual supplied playback selection, never inferred from list order or availability. */
  pickedFileId?: string;
  /** The owner's ordered disc titles from the same authorized read; absent otherwise. */
  disc?: FileInfoDisc;
  continuation?: FileInfoContinuation;
  /** Still preparing (for instance the opening preparation, before its Detail is known): the same
   * panel with quiet version-shaped lines, so nothing moves when the versions arrive. */
  pending?: boolean;
  onManage: (action: FileInfoAction) => void;
  detent?: number;
  onDetentChange?: (index: number) => void;
  scrollRef?: BottomSheetProps['scrollRef'];
};
const t = defaultI18n.t;

/** Context arrays include their own punctuation; inline parts must stay adjacent. */
function Parts({parts}: {parts: readonly FactPart[]}) {
  return parts.map(part => <span key={part.id} data-part={part.id} data-role={part.role}>{part.text}</span>);
}

function More({more, label}: {more: FileInfoMore | undefined; label: string}) {
  if (!more) return null;
  return <div className={styles.more}><Button variant="glass" size="xs" loading={more.busy} onClick={more.onPress}>{more.failed ? t('next.states.tryAgain') : label}</Button></div>;
}

/** Tells the owner of a windowed read when a version is first seen, so its tracks are read then. */
function useVisible(onVisible: (() => void) | undefined) {
  const ref = useRef<HTMLElement>(null), seen = useRef(false), callback = useRef(onVisible);
  callback.current = onVisible;
  useEffect(() => {
    const element = ref.current;
    if (!element || seen.current || !callback.current) return;
    const fire = () => { if (!seen.current) { seen.current = true; callback.current?.(); } };
    if (typeof IntersectionObserver !== 'function') { fire(); return; }
    const observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) { fire(); observer.disconnect(); } }, {rootMargin: '200px 0px'});
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return ref;
}

function Version({file, pickedFileId, continuation}: {file: FileInfoModel['files'][number]; pickedFileId?: string; continuation?: FileInfoContinuation}) {
  const ref = useVisible(continuation?.onVersionVisible && (() => continuation.onVersionVisible!(file.id)));
  const rows = file.rows;
  const row = (id: string) => rows.find(value => value.id === id);
  const resolution = row('resolution');
  const audio = continuation?.tracks?.(file.id, 'audio'), subtitles = continuation?.tracks?.(file.id, 'subtitles');
  const pending = <span className={styles.pending} aria-hidden="true" />;
  return <section ref={ref} className={styles.version} data-file-id={file.id}>
    <h3><span className={styles.versionBadge} data-picked={file.id === pickedFileId || undefined}>{file.display.heading.text}</span><span className={styles.summary}><Parts parts={file.display.summary} /></span></h3>
      <dl>{resolution && <><dt>{resolution.label}</dt><dd><Parts parts={resolution.parts} /></dd></>}
        {file.display.video.length > 0 && <><dt>{t('next.kit.video')}</dt><dd className={styles.technical}><Parts parts={file.display.video} /></dd></>}
        {(file.display.audio.length > 0 || audio?.pending) && <><dt>{t('next.kit.audio')}</dt><dd aria-busy={audio?.pending || undefined}>{file.display.audio.map(track => <div key={track.id} className={styles.track}><Parts parts={track.parts} /></div>)}{audio?.pending && !file.display.audio.length && pending}<More more={audio?.more} label={t('next.overlays.fileInfoMoreTracks')} /></dd></>}
        {(file.display.subtitles.length > 0 || subtitles?.pending) && <><dt>{t('next.kit.subtitles')}</dt><dd aria-busy={subtitles?.pending || undefined}><Parts parts={file.display.subtitles} />{subtitles?.pending && !file.display.subtitles.length && pending}<More more={subtitles?.more} label={t('next.overlays.fileInfoMoreTracks')} /></dd></>}
        {['size', 'length', 'frame-rate'].map(id => { const value = row(id); return value && <Fragment key={id}><dt>{value.label}</dt><dd><Parts parts={value.parts} /></dd></Fragment>; })}
      </dl>
    </section>;
}

function FileInfoBody({model, layout, pickedFileId, disc, continuation, onManage}: Pick<FileInfoProps, 'model' | 'layout' | 'pickedFileId' | 'disc' | 'continuation' | 'onManage'>) {
  const phone = layout === 'phone';
  const heading = (versionId: string) => model.files.find(file => file.id === versionId)?.display.heading.text;
  return <div className={styles.files} data-layout={layout}><More more={continuation?.earlier} label={t('next.overlays.fileInfoFirstVersion')} />{model.files.map(file => <Version key={file.id} file={file} pickedFileId={pickedFileId} continuation={continuation} />)}<More more={continuation?.more} label={t('next.overlays.fileInfoMoreVersions')} /><p className={styles.note}>{t(phone ? 'next.overlays.fileInfoNote' : 'next.overlays.fileInfoDesktopNote')}</p>{disc && disc.titles.length > 0 && <section className={styles.owner} aria-label={t('next.overlays.discTitles')}>
    <div className={styles.ownerHeading}>{phone ? <ServerMark size={36} context="file-info-phone" /> : <ServerMark size={34} />}<div><h3>{t('next.overlays.discTitles')}{!phone && <OwnerTag />}</h3>{!phone && <small>{heading(disc.fileId)}</small>}</div>{phone && <OwnerTag size="phone" />}</div>
    <dl className={styles.ownerLocations}>{disc.titles.map(title => <div key={title.number} className={styles.location}><span className={styles.versionBadge}>{t('next.overlays.discTitle', {number: title.number})}</span><span className={styles.path}>{durationText(title.durationMs / 1000, t) ?? ''}</span><span className={styles.path}>{t('next.overlays.discChapters', {count: title.chapters})}</span></div>)}</dl>
    <p className={styles.note}>{t('next.overlays.discTitlesNote')}</p>
  </section>}{model.owner && <section className={styles.owner} aria-label={t('next.shell.ownerOnly')}>
    <div className={styles.ownerHeading}>{phone ? <ServerMark size={36} context="file-info-phone" /> : <ServerMark size={34} />}<div><h3>{model.owner.heading}{!phone && <OwnerTag />}</h3>{!phone && <small>{t('next.overlays.membersNeverSee')}</small>}</div>{phone && <OwnerTag size="phone" />}</div>
    <div className={styles.ownerLocations}>{model.owner.locations.map((location, index, all) => <Fragment key={location.id}><div className={styles.location}><span className={styles.versionBadge}>{heading(location.versionId)}</span><span className={styles.path}>{location.path}</span>{location.actions.map(action => <Button variant="text" size="small" icon="i-trash" iconSize={phone ? 16 : 17} className={styles.deleteVersion} key={action.id} onClick={() => onManage({fileId: location.id, action: action.id})}>{action.label}</Button>)}</div>{all[index + 1]?.versionId !== location.versionId && <More more={continuation?.locationsMore?.(location.versionId)} label={t('next.overlays.fileInfoMoreFiles')} />}</Fragment>)}
    <div className={styles.ownerActions}>{model.owner.actions.map(action => <Button variant="text" size="small" iconSize={phone ? 18 : 17} icon={action.id === 'split-versions' ? 'i-split' : 'i-merge'} key={action.id} onClick={() => onManage({action: action.id})}>{action.label}</Button>)}</div></div><p className={styles.note}>{t('next.overlays.deleteVersionNote')}</p>
  </section>}</div>;
}

/** Version-card shaped quiet lines in the panel's own layout; no shimmer (motion rules). */
function PendingBody({layout}: {layout: FileInfoProps['layout']}) {
  const line = <span className={styles.pending} />;
  return <div className={styles.files} data-layout={layout} aria-busy="true">{[0, 1].map(version => <section key={version} className={styles.version} aria-hidden="true">
    <h3>{line}</h3><dl>{[0, 1, 2, 3].map(row => <Fragment key={row}><dt>{line}</dt><dd>{line}</dd></Fragment>)}</dl>
  </section>)}</div>;
}

/** The model supplies authorized facts/actions; this renderer only emits real callbacks. */
export function FileInfo({model, subject, layout, pickedFileId, disc, continuation, pending = false, onManage, detent, onDetentChange, scrollRef, ...surface}: FileInfoProps) {
  const [scrollBody, setScrollBody] = useState<HTMLDivElement | null>(null), [scrolled, setScrolled] = useState(false);
  const bodyRef = useCallback((element: HTMLDivElement | null) => {
    setScrollBody(element);
    if (typeof scrollRef === 'function') return scrollRef(element);
    if (scrollRef) scrollRef.current = element;
  }, [scrollRef]);
  useLayoutEffect(() => {
    if (!scrollBody) return;
    const update = () => setScrolled(scrollBody.scrollTop > 0);
    update(); scrollBody.addEventListener('scroll', update, {passive: true});
    return () => scrollBody.removeEventListener('scroll', update);
  }, [scrollBody]);
  const body = pending ? <PendingBody layout={layout} /> : <FileInfoBody model={model} layout={layout} pickedFileId={pickedFileId} disc={disc} continuation={continuation} onManage={onManage} />;
  if (layout === 'phone') return <BottomSheet {...surface} title={<>{model.heading}<small className={styles.subject}>{subject}</small></>} className={[styles.sheet, scrolled ? styles.scrolled : undefined].filter(Boolean).join(' ')} headerClassName={styles.sheetHeader} bodyClassName={styles.sheetBody} detents={[554, 740]} detent={detent} onDetentChange={onDetentChange} scrollRef={bodyRef}>{body}</BottomSheet>;
  return <Dialog {...surface} title={model.heading} description={<>{subject}{!pending && <span className={styles.count}>{t('next.overlays.versionCount', {count: continuation?.versionCount ?? model.files.length})}</span>}</>} responsive={false} placement="right" width={560} closeIconSize={22} closeLabel={t('next.overlays.closeFileInfo')} className={styles.panel} headerClassName={styles.panelHeader} bodyClassName={styles.panelBody}>{body}</Dialog>;
}
