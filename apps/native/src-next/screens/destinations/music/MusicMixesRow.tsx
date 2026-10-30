import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {ScrollView, StyleSheet, Text, View} from 'react-native';
import Svg, {Defs, RadialGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Artwork} from '../../../kit/artwork';
import {Focusable} from '../../../kit/controls';
import {useTheme} from '../../../kit/theme';
import {browseType} from '../../../kit/browse/presentation';
import {FocusRegion} from '../../../kit/focus';
import {MenuRow, SidePanel} from '../../../kit/overlays';
import type {NativeMusicMixRow, NativeMusicMixesCaller, NativeMusicMixesPort} from './mixes';

const t = defaultI18n.t;
/** music-plus 15: three covers fanned at the top right, each a little smaller than the card's
 * half, with the mix's own style set large inside the cover and its name below. */
const fanAngles = [-12, -1, 11] as const, fanLefts = [0, .22, .44] as const, fanTops = [.1, 0, .08] as const;

export function MusicMixCard({row, artwork, width = 186, onPlay, onFocus, onMenu, cardRef, disabled}: {
  row: NativeMusicMixRow; artwork: (path: string | undefined) => React.ComponentProps<typeof Artwork>['source']; width?: number; onPlay: () => void;
  onFocus?: () => void; onMenu?: () => void; cardRef?: RefObject<View | null>; disabled?: boolean;
}) {
  const theme = useTheme(), tv = theme.isTV, phone = theme.platform === 'phone';
  // Each platform keeps its own drawn card: kit.css' card art radius, music-plus' per-platform
  // style word, title, caption and fan offsets, and Home's track gap.
  const radius = tv ? 14 : 10, fanWidth = width * .62, fanHeight = width * .52, cover = fanWidth * .56;
  const styleSize = tv ? 32 : phone ? 18 : 26;
  const name: Readonly<{size: number; weight: 400 | 500 | 600 | 700 | 800; top: number; color: string}> =
    tv ? {size: 22, weight: 700, top: 14, color: theme.colors.textSecondary} : phone ? {size: 13, weight: 600, top: 8, color: theme.colors.text} : {size: 14, weight: 600, top: 10, color: theme.colors.text};
  const caption = tv ? {size: 20, top: 3} : phone ? {size: 12, top: 2} : {size: 13, top: 2};
  const at = tv ? {left: 18, right: 18, bottom: 16} : phone ? {left: 10, right: 10, bottom: 9} : {left: 14, right: 14, bottom: 12};
  const gradient = `mix${row.id.replace(/[^a-zA-Z0-9]/g, '')}`;
  // Frame 15's artist line ends with the bare "and more" the catalogue now carries.
  const artists = defaultI18n.list([...row.artists.map(artist => artist.name), ...(row.more ? [t('music.mixesAndMore')] : [])], 'and');
  return <Focusable ref={cardRef} disabled={disabled} onFocus={onFocus} onLongPressAction={onMenu} focusId={`music-mix:${row.id}`} testID={`music-mix:${row.id}`} focusKind="card" focusRadius={radius}
    accessibilityRole="button" accessibilityLabel={t('music.mix.name', {name: row.name})} onPress={onPlay} style={{width, marginRight: tv ? 28 : phone ? 12 : 16}}>
    <View style={{width, height: width, borderRadius: radius, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
      <Svg width={width} height={width} style={StyleSheet.absoluteFill} pointerEvents="none">
        <Defs><RadialGradient id={gradient} cx="100%" cy="0%" rx="130%" ry="120%"><Stop offset="0" stopColor={row.tint}/><Stop offset="0.72" stopColor={row.tint} stopOpacity={0}/></RadialGradient></Defs>
        <Rect width="100%" height="100%" fill={`url(#${gradient})`}/>
      </Svg>
      <View pointerEvents="none" style={{position: 'absolute', right: phone ? 8 : 12, top: phone ? 10 : 14, width: fanWidth, height: fanHeight}}>
        {row.covers.slice(0, 3).map((coverAt, index) => {
          const source = artwork(coverAt.posterUrl);
          if (!source) return null;
          return <View key={coverAt.id} style={{position: 'absolute', left: fanWidth * fanLefts[index]!, top: fanHeight * fanTops[index]!, width: cover, height: cover,
            borderRadius: 6, overflow: 'hidden', zIndex: index, borderWidth: 1, borderColor: 'rgba(237,245,250,.16)',
            transform: [{rotate: `${fanAngles[index]}deg`}], shadowColor: theme.colors.shadow, shadowOpacity: .85, shadowRadius: 22, shadowOffset: {width: 0, height: 12}}}>
            <Artwork source={source} width={cover} height={cover}/>
          </View>;
        })}
      </View>
      <Text numberOfLines={2} style={[browseType(theme, styleSize, 800, -.035, styleSize), {position: 'absolute', left: at.left, right: at.right, bottom: at.bottom,
        color: theme.colors.white, textShadowColor: 'rgba(7,16,24,.6)', textShadowOffset: {width: 0, height: 2}, textShadowRadius: 18}]}>{row.name}</Text>
      <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: radius, borderWidth: 1, borderColor: 'rgba(237,245,250,.08)'}]}/>
    </View>
    <Text numberOfLines={2} style={[browseType(theme, name.size, name.weight), {marginTop: name.top, color: name.color}]}>{t('music.mix.name', {name: row.name})}</Text>
    <Text numberOfLines={2} style={[browseType(theme, caption.size, 400, 0, caption.size * 1.35),
      {marginTop: caption.top, minHeight: caption.size * 1.35 * 2, color: theme.colors.textTertiary}]}>{artists}</Text>
  </Focusable>;
}

/** Mixes for you: the server's own profile row, absent until it has something real to draw. */
export function MusicMixesRow({port, artwork, reportError, title = t('music.mixesForYou'), onOpen, marginHorizontal, active = true, focus, focusScope, onCardFocus, onReady}: {
  port: NativeMusicMixesPort; artwork: (path: string | undefined) => React.ComponentProps<typeof Artwork>['source'];
  reportError: (error: unknown) => void; title?: string; onOpen?: () => void; marginHorizontal?: number; active?: boolean;
  focusScope?: string; focus?: Readonly<{id: string; up: string; down?: string; left: string}>; onCardFocus?: (id: string) => void;
  onReady?: () => void;
}) {
  const theme = useTheme(), snapshot = useSyncExternalStore(port.subscribe, port.getSnapshot);
  const revision = useSyncExternalStore(port.subscribeRevision, port.getRevision);
  const [caller, setCaller] = useState<NativeMusicMixesCaller | null>(null);
  const [menu, setMenu] = useState<Readonly<{row: NativeMusicMixRow; caller: NativeMusicMixesCaller; ref: RefObject<View | null>; scope: string | undefined}> | undefined>();
  const [busy, setBusy] = useState(false);
  const firstAction = useRef<View>(null);
  useEffect(() => {setMenu(undefined); setBusy(false);}, [revision, active]);
  useEffect(() => {setMenu(value => value && snapshot.rows.includes(value.row) ? value : undefined);}, [snapshot]);
  // The row holds the route and revision that committed it. A push or Back renews the lease,
  // and while the route is not Home or Discover no lease exists, so a retained hidden card
  // can never borrow the new route's caller.
  useLayoutEffect(() => {
    const captured = active ? port.captureCaller() : undefined;
    setCaller(captured ?? null);
    return () => {captured?.dispose();};
  }, [port, revision, active]);
  useEffect(() => {if (snapshot.phase === 'idle') void port.load().catch(() => {});}, [port, snapshot.phase]);
  useEffect(() => {if (active && snapshot.phase === 'ready' && snapshot.rows.length) onReady?.();}, [active, snapshot, onReady]);
  if (snapshot.phase !== 'ready' || !snapshot.rows.length) return null;
  const play = (row: NativeMusicMixRow) => {
    if (!caller) return;
    void port.play(row, caller.lifetime).catch(error => reportError(error));
  };
  const heading = theme.isTV ? theme.type.rowHeading : browseType(theme, 20, 800, -.02);
  const sub = browseType(theme, theme.isTV ? 20 : 13, 400);
  const command = (kind: 'play' | 'shuffle' | 'save') => {
    const opening = menu; if (!opening || busy || !opening.caller.lifetime.isCurrent()) return;
    setBusy(true);
    void (kind === 'save' ? port.save(opening.row, opening.caller.lifetime) : port.play(opening.row, opening.caller.lifetime, kind === 'shuffle'))
      .then(() => {if (opening.caller.lifetime.isCurrent()) setMenu(undefined);})
      .catch(error => {if (opening.caller.lifetime.isCurrent()) reportError(error);})
      .finally(() => {if (opening.caller.lifetime.isCurrent()) setBusy(false);});
  };
  const cards = <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: marginHorizontal ?? (theme.isTV ? 96 : 16), paddingVertical: theme.isTV ? 16 : 0}}>
    {snapshot.rows.map(row => <MusicMixTile key={row.id} row={row} artwork={artwork} width={theme.isTV ? 200 : theme.platform === 'tablet' ? 210 : 186}
      disabled={!active || !caller} onFocus={() => onCardFocus?.(`music-mix:${row.id}`)} onPlay={() => play(row)}
      onMenu={ref => {if (caller?.lifetime.isCurrent()) setMenu({row, caller, ref, scope: focus?.id ?? focusScope});}}/>)}
  </ScrollView>;
  const headingButton = onOpen && <Focusable disabled={!active} focusId="music-mixes-open" testID="music-mixes-open" accessibilityRole="button" accessibilityLabel={title} onPress={onOpen}
    style={{marginHorizontal: marginHorizontal ?? (theme.isTV ? 96 : 16), marginBottom: 12, flexDirection: 'row', alignItems: 'baseline', gap: 16}}>
    <Text style={[heading, {color: theme.colors.text}]}>{title}</Text>
    <Text style={[sub, {color: theme.colors.textTertiary}]}>{t('music.mixesFromListening')}</Text>
  </Focusable>;
  return <View testID="music-mixes-row" style={{marginTop: theme.isTV ? 44 : theme.layoutMode === 'phone' ? 28 : 26}}>
    {onOpen ? focus ? <FocusRegion id={`${focus.id}.more`} enabled={active} entry="music-mixes-open" order={['music-mixes-open']}
      edges={{up: focus.up, down: focus.id, left: focus.left}}>{headingButton}</FocusRegion> : headingButton : <View style={{marginHorizontal: theme.isTV ? 96 : 16, marginBottom: 12, flexDirection: 'row', alignItems: 'baseline', gap: 16}}>
      <Text style={[heading, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[sub, {color: theme.colors.textTertiary}]}>{t('music.mixesFromListening')}</Text>
    </View>}
    {focus ? <FocusRegion id={focus.id} enabled={active} orientation="horizontal" entry={`music-mix:${snapshot.rows[0]!.id}`}
      order={snapshot.rows.map(row => `music-mix:${row.id}`)} edges={{up: onOpen ? `${focus.id}.more` : focus.up, down: focus.down, left: focus.left}}>{cards}</FocusRegion> : cards}
    {menu && active && menu.caller.lifetime.isCurrent() && <SidePanel testID={menu.scope ? `${menu.scope}.${menu.row.id}.menu` : undefined} open title={t('music.mix.name', {name: menu.row.name})}
      onOpenChange={open => {if (!open && !busy) setMenu(undefined);}} returnFocusRef={menu.ref} initialFocusRef={firstAction} showClose={false} dismissOnBack={!busy} width={640}>
      <MenuRow ref={firstAction} focusId="play" label={t('entry.play')} icon="i-play" disabled={busy} onPress={() => command('play')}/>
      <MenuRow focusId="shuffle" label={t('title.shuffle')} icon="i-shuffle" disabled={busy} onPress={() => command('shuffle')}/>
      <MenuRow focusId="save" label={t('player.queueSave')} icon="i-listplus" disabled={busy} onPress={() => command('save')}/>
    </SidePanel>}
  </View>;
}

function MusicMixTile({onMenu, ...props}: Omit<React.ComponentProps<typeof MusicMixCard>, 'onMenu' | 'cardRef'> & {onMenu: (ref: RefObject<View | null>) => void}) {
  const ref = useRef<View>(null);
  return <MusicMixCard {...props} cardRef={ref} onMenu={() => onMenu(ref)}/>;
}
