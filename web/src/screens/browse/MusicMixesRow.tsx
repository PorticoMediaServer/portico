import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {musicMixDisplayName} from '@core/music-mixes.ts';
import {Shelf} from '../../kit/layout';
import {SquareCard} from '../../kit/cards';
import {Artwork} from '../../kit/artwork';
import {Menu, MenuItem} from '../../kit/overlays';
import {useShellWidth} from '../../shell/viewport';
import type {ShellArtwork} from '../../shell/artwork';
import type {MusicMixRow} from './music-mixes';
import type {MusicMixesBinding} from './music-mixes-binding';
import {readMusicMixesForView} from './music-mixes-view-read';
import styles from './music-mixes.module.css';

const t = defaultI18n.t;
/** Home and Discover render the same profile-owned source. Library identity and
 * navigation never become a predicate on this row or its captured commands. */
export function MusicMixesRow({binding, artwork, more, empty, target = false, reducedMotion, reportError, sizeClass}: {
  binding: MusicMixesBinding; artwork: ShellArtwork; more?: ReactNode; empty?: ReactNode; target?: boolean; reducedMotion: boolean; reportError: (error: unknown) => void;
  sizeClass?: 'medium';
}) {
  const snapshot = useSyncExternalStore(binding.subscribe, binding.getSnapshot, binding.getSnapshot), width = useShellWidth();
  const root = useRef<HTMLDivElement>(null), targeted = useRef<MusicMixesBinding | undefined>(undefined);
  const [menu, setMenu] = useState<Readonly<{row: MusicMixRow; anchor: HTMLButtonElement}>>();
  const menuRef = useRef<typeof menu>(undefined);menuRef.current = menu;
  const ready = snapshot.phase === 'ready' && snapshot.rows.length > 0;
  useLayoutEffect(() => readMusicMixesForView(binding, reportError), [binding, reportError]);
  useEffect(() => {
    if (!target || !ready || targeted.current === binding) return;
    const element = root.current, button = element?.querySelector<HTMLButtonElement>('article button');
    if (!element || !button) return;
    targeted.current = binding;element.scrollIntoView({block: 'start', behavior: 'instant'});button.focus({preventScroll: true});
  }, [target, ready, binding]);
  const shown = menu && binding.contains(menu.row) ? menu : undefined;
  if (!ready) return <>{empty}</>;
  const platform = width >= 1024 ? 'desktop' : 'phone-web', cardWidth = platform === 'desktop' ? 190 : sizeClass === 'medium' ? 152 : 132;
  const run = (row: MusicMixRow, action: 'play' | 'shuffle' | 'save') => {
    if (!binding.contains(row)) return;
    void (action === 'save' ? binding.save(row) : binding.play(row, action === 'shuffle')).catch(reportError);
  };
  return <div ref={root} className={styles.target} data-music-mixes>
    <Shelf focusScope={binding} heading={t('music.mixesForYou')} subtitle={platform === 'desktop' ? t('music.mixesFromListening') : undefined}
      actions={more} platform={platform} sizeClass={sizeClass} reducedMotion={reducedMotion} itemWidth={cardWidth} items={snapshot.rows} getKey={row => row.mix.id}
      renderItem={(row, position) => {
        const artists = row.mix.artists.map(artist => artist.name.replace(/ /g, '\u00a0')).join(', ') + (row.mix.more ? ` ${t('music.mixesAndMore')}` : '');
        return <SquareCard className={styles.card} model={{title: row.name, caption: [{id: 'artists', text: artists, role: 'identity'}]}}
          style={{'--cards-width': `${cardWidth}px`, '--mix-tint': row.mix.tint} as CSSProperties} visible
          onNavigate={() => run(row, 'play')} onPlay={binding.canPlay(row) ? () => run(row, 'play') : undefined}
          onMore={anchor => setMenu({row, anchor})} moreExpanded={shown?.row === row}
          fallback={<span className={styles.cover}><span className={styles.fan}>{row.mix.covers.map((cover, index) => <span key={`${cover.album.id}:${index}`}>
            <Artwork source={artwork.foreground(cover.posterUrl, cardWidth, 1, {group: 'music-mixes', order: position.index * 3 + index})} alt="" visible/>
          </span>)}</span><span className={styles.name}>{musicMixDisplayName(row.mix.nameSource, row.mix.artists, t, 'label')}</span></span>}/>;
      }}/>
    {shown && <Menu open title={shown.row.name} anchorRef={{current: shown.anchor}} returnFocusRef={{current: shown.anchor}}
      onOpenChange={open => {if (!open) setMenu(undefined);}}>
      <MenuItem icon="i-play" disabled={!binding.canPlay(shown.row)} onSelect={() => {if (menuRef.current === shown) run(shown.row, 'play');}}>{t('next.controls.play')}</MenuItem>
      <MenuItem icon="i-shuffle" disabled={!binding.canPlay(shown.row)} onSelect={() => {if (menuRef.current === shown) run(shown.row, 'shuffle');}}>{t('next.dock.shuffle')}</MenuItem>
      <MenuItem icon="i-plus" onSelect={() => {if (menuRef.current === shown) run(shown.row, 'save');}}>{t('player.queueSave')}</MenuItem>
    </Menu>}
  </div>;
}
