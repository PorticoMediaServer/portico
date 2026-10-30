import {useRef, type CSSProperties, type KeyboardEvent} from 'react';
import {profileArtColors} from '@design';
import {defaultI18n} from '@i18n';
import type {SelectionProfile} from '@core/session-selection';
import {Icon} from '../Icon';
import {artClass, artSize} from './Artwork';
import type {ArtworkPalette, ArtStyleProps} from './types';
import styles from './artwork.module.css';

export type ProfileFaceProps = ArtStyleProps & Readonly<{
  initial: string; art?: keyof typeof profileArtColors; locked?: boolean; selected?: boolean;
  size?: number; alt?: string;
  /** Explicit presentation base; omitted for the canonical semantic art colour. */
  tint?: string;
  /** Explicit two-stop 135-degree presentation; omitted for the semantic/tint recipe. */
  palette?: ArtworkPalette;
}>;
export function ProfileFace({initial, art = 'blue', tint, palette, locked = false, selected = false, size, alt = '', className, style, width = size, height = size}: ProfileFaceProps) {
  return <span className={artClass(styles.profileFace, palette && styles.profilePalette, selected && styles.currentFace, className)} style={artSize({width, height, style: {'--profile-colour': tint ?? profileArtColors[art], ...(palette ? {'--profile-first': palette.first, '--profile-second': palette.second} : {}), ...(size ? {'--profile-size': `${size}px`} : {}), ...style} as CSSProperties})} role={alt ? 'img' : undefined} aria-label={alt || undefined} aria-hidden={alt ? undefined : true}>
    {initial}{locked && <span className={styles.lockBadge}><Icon name="i-lock" /></span>}
  </span>;
}

export type ProfilePickerProfile = SelectionProfile & Readonly<{initial?: string}>;
export type ProfilePickerProps = ArtStyleProps & Readonly<{
  profiles: readonly ProfilePickerProfile[]; onSelect: (profile: ProfilePickerProfile) => void;
  selectedId?: string; onAdd?: () => void; onManage?: () => void;
  layout?: 'auto' | 'desktop' | 'phone' | 'tv'; autoFocusId?: string;
  onFocusChange?: (id: string) => void;
  /** Optional context-specific presentation; never changes a profile or its selection payload. */
  getProfileTint?: (profile: ProfilePickerProfile) => string | undefined;
}>;
function profileArt(art: string | undefined): keyof typeof profileArtColors {
  return art && Object.hasOwn(profileArtColors, art) ? art as keyof typeof profileArtColors : 'blue';
}
function badge(profile: ProfilePickerProfile) {
  if (!profile.eligible) return defaultI18n.t(profile.unavailableReason === 'server_restriction' ? 'next.artwork.restricted' : 'next.artwork.notShared');
  if (profile.primary) return defaultI18n.t('next.artwork.mainProfile');
  return profile.pinRequired ? defaultI18n.t('next.artwork.protected') : undefined;
}
export function ProfilePicker({profiles, onSelect, selectedId, onAdd, onManage, layout = 'auto', autoFocusId, onFocusChange, getProfileTint, className, style, width, height}: ProfilePickerProps) {
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const chooseFocus = (event: KeyboardEvent<HTMLButtonElement>, id: string) => {
    const delta = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0;
    if (!delta || layout !== 'tv') return;
    const available = profiles.filter(profile => profile.eligible).map(profile => profile.id);
    const index = available.indexOf(id), next = available[index + delta];
    if (next) { event.preventDefault(); buttons.current.get(next)?.focus(); }
  };
  return <div className={artClass(styles.picker, styles[`picker${layout}`], className)} style={artSize({width, height, style})}>
    <div className={styles.profileRow}>
      {profiles.map(profile => <button key={profile.id} type="button" disabled={!profile.eligible} autoFocus={profile.id === autoFocusId} data-selected={profile.id === selectedId || undefined}
        ref={node => { if (node) buttons.current.set(profile.id, node); else buttons.current.delete(profile.id); }}
        className={styles.profileTile} onClick={() => onSelect(profile)} onFocus={() => onFocusChange?.(profile.id)} onKeyDown={event => chooseFocus(event, profile.id)}>
        <ProfileFace initial={profile.initial ?? Array.from(profile.name.trim())[0] ?? ''} art={profileArt(profile.art)} tint={getProfileTint?.(profile)} locked={profile.pinRequired} selected={profile.id === selectedId} />
        <b className={styles.profileName}>{profile.name}</b>
        <small className={styles.profileBadge}>{profile.pinRequired && profile.eligible && !profile.primary ? <><span className={styles.fullPin}>{defaultI18n.t('next.artwork.protected')}</span><span className={styles.shortPin}>{defaultI18n.t('next.artwork.pin')}</span></> : badge(profile)}</small>
      </button>)}
      {onAdd && layout !== 'tv' && <button type="button" className={artClass(styles.profileTile, styles.addProfile)} onClick={onAdd}>
        <span className={styles.profileFace}><Icon name="i-plus" size={56} /></span><b className={styles.profileName}>{defaultI18n.t('next.artwork.addProfile')}</b><small className={styles.profileBadge} />
      </button>}
    </div>
    {onManage && layout !== 'tv' && <button type="button" className={styles.manageProfiles} onClick={onManage}><Icon name="i-pencil" size={20} />{defaultI18n.t('next.artwork.manageProfiles')}</button>}
  </div>;
}
