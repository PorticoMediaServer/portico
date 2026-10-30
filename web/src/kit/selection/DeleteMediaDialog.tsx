import {useRef} from 'react';
import {defaultI18n} from '@i18n';
import type {DeletePreview} from '@core/administration';
import {Button, Switch, TextField} from '../controls';
import {Dialog} from '../overlays';
import {usePhone} from '../overlays/shared';
import {Icon} from '../Icon';
import {selectionText as text} from './copy';
import {deletePermission} from './presentation';
import type {DeleteMediaDialogProps} from './types';
import styles from './selection.module.css';

function PreviewDependents({preview, concise = false}: {preview: DeletePreview; concise?: boolean}) {
  const target = preview.targets[0];
  if (!target) return null;
  if (preview.targets.length !== 1) {
    const counts = {playlist: 0, collection: 0, progress: 0};
    for (const item of preview.targets) for (const dependent of item.dependents) if (Object.hasOwn(counts, dependent.kind)) counts[dependent.kind as keyof typeof counts] += dependent.count;
    return <>{text('next.selection.dependentsBulk', {playlists: counts.playlist, collections: counts.collection, savedPlaces: counts.progress})}</>;
  }
  const places: string[] = [], other: string[] = [];
  let profiles = 0;
  for (const dependent of target.dependents) {
    if (dependent.count <= 0) continue;
    if (dependent.kind === 'playlist') places.push(text('next.selection.playlistsCount', {count: dependent.count}));
    else if (dependent.kind === 'collection') places.push(text('next.selection.collectionsCount', {count: dependent.count}));
    else if (dependent.kind === 'progress') profiles += dependent.count;
    else other.push(text('next.selection.dependent', {count: dependent.count, description: dependent.description}));
  }
  return <>
    {places.length > 0 && profiles > 0 ? <>{text('next.selection.dependentsOne', {places: defaultI18n.list(places), profiles: text('next.selection.profilesCount', {count: profiles})})} {!concise && text('next.selection.entriesGo')}</> : <>
      {places.length > 0 && <>{text('next.selection.alsoIn', {places: defaultI18n.list(places)})} </>}
      {profiles > 0 && <>{text('next.selection.savedPlaces', {profiles: text('next.selection.profilesCount', {count: profiles})})} </>}
    </>}
    {other.map((label, index) => <span key={index}> {label}</span>)}
  </>;
}

/** No destructive callback can fire without the actual preview and its exact typed confirmation. */
export function DeleteMediaDialog(props: DeleteMediaDialogProps) {
  const {owner, selectionCount, preview, confirmation, onConfirmationChange, removeFiles, onRemoveFilesChange, onDelete, busy = false, error, result, ownerTag, artwork, targetFacts, ...surface} = props;
  const input = useRef<HTMLInputElement | null>(null), phone = usePhone();
  if (!owner) return null;
  const target = preview?.targets[0], tooMany = selectionCount > 200;
  const blocked = preview?.targets.filter(entry => !entry.allowMediaDeletion || preview.blocked.includes(entry.libraryId)) ?? [];
  const acrossLibraries = preview ? new Set(preview.targets.map(entry => entry.libraryId)).size > 1 : false;
  const allowed = deletePermission(owner, selectionCount, preview, confirmation, busy) && !error;
  const title = result ? text('next.selection.done') : target && selectionCount === 1 ? text('next.selection.deleteTitle', {title: target.title}) : text('next.selection.deleteTitles', {count: selectionCount});
  const failures = result?.receipts.filter(receipt => !receipt.removed) ?? [];
  const footer = <>
    <Button size="compact" disabled={busy} variant={result ? 'primary' : 'glass'} onClick={() => surface.onOpenChange(false, 'action')}>{text(result || tooMany || blocked.length || acrossLibraries || error ? 'next.selection.close' : 'next.selection.cancel')}</Button>
    {!result && <Button size="compact" className={styles.deleteButton} disabled={!allowed} loading={busy} onClick={() => { if (preview && allowed && deletePermission(owner, selectionCount, preview, confirmation)) onDelete({preview, confirmation, deleteFiles: removeFiles}); }}>{text(selectionCount === 1 || tooMany ? 'next.selection.delete' : 'next.selection.deleteCount', {count: selectionCount})}</Button>}
  </>;
  return <Dialog {...surface} title={<span className={styles.deleteHeading}><span className={styles.deleteOwner}>{ownerTag}</span><span>{title}</span></span>} accessibilityLabel={title} width={520} className={styles.deleteDialog} bodyClassName={styles.deleteBody} footerClassName={styles.deleteFooter} initialFocusRef={input} footer={footer} dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy && !phone}>
    {result ? <>
      <p role="status">{text('next.selection.deleteResult', {removed: result.removed, failed: result.failed})}</p>
      {failures.slice(0, 5).map(receipt => <div key={receipt.itemId} className={styles.deleteNotice} data-tone="warning"><Icon name="i-warn" size={20} /><div><b>{receipt.title}</b><p>{text(receipt.code === 'file_changed' ? 'next.selection.fileChanged' : receipt.code === 'file_not_removed' ? 'next.selection.fileNotRemoved' : 'next.selection.fileKept')}</p></div></div>)}
      {failures.length > 5 && <p className={styles.deleteSmall}>{text('next.selection.andMore', {count: failures.length - 5})}</p>}
      {result.filesRetained && <p className={styles.deleteSmall}>{text('next.selection.recordOnly')}</p>}
    </> : tooMany ? <>
      <div className={styles.deleteNotice} data-tone="error"><Icon name="i-warn" size={20} /><p>{text('next.selection.tooMany')}</p></div><p className={styles.deleteSmall}>{text('next.selection.neverSplit')}</p>
    </> : blocked.length > 0 || preview?.blocked.length || acrossLibraries ? <div className={styles.deleteNotice} data-tone="warning"><Icon name="i-warn" size={20} /><p>{acrossLibraries ? text('next.selection.oneLibrary') : text('next.selection.blockedLibrary', {library: defaultI18n.list(blocked.map(entry => entry.libraryName))})}</p></div> : preview ? <>
      {target && target.titles > 1 && <p><b>{text('next.selection.titlesRemoved', {count: target.titles})}</b></p>}
      {selectionCount > 1 && removeFiles && !phone && <div className={styles.deleteTargets}>{preview.targets.slice(0, 5).map(entry => <div key={entry.itemId}><span className={styles.deleteArtwork}>{artwork?.[entry.itemId]}</span><span><b>{entry.title}</b><small>{targetFacts?.[entry.itemId] ?? text('next.selection.files', {count: entry.files.length, size: defaultI18n.bytes(entry.bytes)})}</small></span></div>)}{preview.targets.length > 5 && <p>{text('next.selection.andMore', {count: preview.targets.length - 5})}</p>}</div>}
      <p><b>{text('next.selection.files', {count: preview.totalFiles, size: preview.totalBytesText})}</b> {target?.kind === 'show' && target.titles > 1 && text('next.selection.showContents')} {selectionCount > 1 && (phone || !removeFiles) ? preview.targets.some(entry => entry.dependents.some(dependent => dependent.count > 0)) && text('next.selection.dependentsMany') : <PreviewDependents preview={preview} concise={phone} />}</p>
      <label className={styles.removeFilesRow}><span>{text('next.selection.removeFiles')}</span><Switch checked={removeFiles} disabled={busy} onCheckedChange={onRemoveFilesChange} aria-label={text('next.selection.removeFiles')} /></label>
      {!removeFiles ? <p className={styles.deleteSmall}>{text('next.selection.recordOnly')}</p> : target?.titles === 1 && (!phone || selectionCount === 1) && <p className={styles.deleteSmall}>{text('next.selection.filesDeleted')}</p>}
      {preview.targets.some(entry => entry.files.some(file => file.shared)) && <p className={styles.deleteSmall}>{text('next.selection.sharedFiles')}</p>}
      <TextField ref={input} containerClassName={styles.deleteConfirmation} label={text(preview.confirmationKind === 'title' ? 'next.selection.typeTitle' : 'next.selection.confirmCount', {count: preview.confirmation})} aria-label={text(preview.confirmationKind === 'title' ? 'next.selection.typeTitle' : 'next.selection.confirmCount', {count: preview.confirmation})} value={confirmation} onChange={event => onConfirmationChange(event.currentTarget.value)} disabled={busy} autoComplete="off" autoCapitalize="none" spellCheck={false} inputMode={preview.confirmationKind === 'count' ? 'numeric' : 'text'} description={preview.confirmationKind === 'title' ? preview.confirmation : undefined} />
    </> : !error && <p role="status">{text('next.selection.checking')}</p>}
    {error && <div className={styles.deleteNotice} data-tone="error" role="alert"><Icon name="i-warn" size={20} /><p>{error}</p></div>}
  </Dialog>;
}
