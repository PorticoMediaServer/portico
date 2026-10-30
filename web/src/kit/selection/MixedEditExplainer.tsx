import type {RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../controls';
import {BottomSheet, Popover} from '../overlays';
import {usePhone} from '../overlays/shared';
import {Icon, type IconName} from '../Icon';
import {selectionText as text} from './copy';
import {selectionCountText} from './presentation';
import type {SelectionMediaKind, SelectionSurface, SelectionTypeCount} from './types';
import styles from './selection.module.css';

export type MixedEditExplainerProps = SelectionSurface & Readonly<{anchorRef: RefObject<HTMLElement | null>; types: readonly SelectionTypeCount[]; reason?: 'mixed-types' | 'no-bulk-fields'; onKeepOnly: (kind: SelectionMediaKind) => void}>;
const typeCopy = {
  movie: 'next.selection.mixedMovies', episode: 'next.selection.mixedEpisodes', album: 'next.selection.mixedAlbums', song: 'next.selection.mixedSongs',
} as const;
const typeIcons: Partial<Record<SelectionMediaKind, IconName>> = {movie: 'i-film', show: 'i-tv', episode: 'i-tv', album: 'i-album', song: 'i-note'};
const keepCopy = {movie: 'next.selection.keepMovie', episode: 'next.selection.keepEpisode', album: 'next.selection.keepAlbum', song: 'next.selection.keepSong'} as const;

export function MixedEditExplainer({types, onKeepOnly, anchorRef, reason = 'mixed-types', ...surface}: MixedEditExplainerProps) {
  const phone = usePhone(), total = types.reduce((sum, type) => sum + type.count, 0);
  const title = text('next.selection.editOneType');
  const children = <div className={styles.mixedBody}>
    {!phone && <h4>{title}</h4>}
    <p>{reason === 'no-bulk-fields' ? text('next.selection.noSeasonFields') : text(phone ? 'next.selection.mixedPhoneExplanation' : 'next.selection.mixedExplanation', {types: defaultI18n.list(types.map(type => Object.hasOwn(typeCopy, type.kind) ? text(typeCopy[type.kind as keyof typeof typeCopy], {count: type.count}) : selectionCountText(type)))})}</p>
    {reason === 'mixed-types' && <><div className={styles.keepLabel}>{text('next.selection.keepOnly')}</div>
      <div className={styles.keepTypes}>{types.map(type => <Button key={type.kind} variant={phone ? 'text' : 'glass'} size="small" icon={phone ? typeIcons[type.kind] : undefined} iconSize={24} className={styles.keepType} onClick={() => onKeepOnly(type.kind)}>{phone && type.count === 1 && Object.hasOwn(keepCopy, type.kind) ? text(keepCopy[type.kind as keyof typeof keepCopy]) : selectionCountText(type)}{phone && <small>{text('next.selection.countOf', {count: type.count, total})}</small>}</Button>)}</div></>}
    {phone && <Button size="compact" className={styles.mixedClose} onClick={() => surface.onOpenChange(false, 'action')}>{text('next.selection.close')}</Button>}
  </div>;
  return phone ? <BottomSheet {...surface} returnFocusRef={surface.returnFocusRef ?? anchorRef} title={title} showCloseButton={false} fitContent className={styles.mixedSheet}>{children}</BottomSheet> : <Popover {...surface} anchorRef={anchorRef} title={title} accessibilityLabel={text('next.selection.editNeedsType')} width={420} placement="bottom-start" className={styles.mixedPopover}>{children}</Popover>;
}
