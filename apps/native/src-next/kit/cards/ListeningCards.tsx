import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import type {ChannelMarkModel, FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {AmbientArt, ChannelMark, MediaArt, type AmbientArtSource, type ArtworkPalette, type ArtworkProps, type ArtworkSource} from '../artwork/index';
import {Button} from '../controls/index';
import {useTheme} from '../theme';
import {CardOverlays} from './MediaCards';
import {ArtFrame, CardFrame, Parts, Progress, alpha, titleStyle, type CardProps} from './primitives';
import {sessionViewerParts, type SessionViewer} from './session-identity';

export type ResumeCardProps = CardProps & {
  artwork?: ArtworkSource | null;
  artworkFallback?: ReactNode;
  artworkProps?: Omit<ArtworkProps, 'source' | 'fallback' | 'width' | 'height'>;
  ambient?: AmbientArtSource;
  palette?: ArtworkPalette;
  progress?: number;
  height?: number;
};
export type ReadResumeCardProps = ResumeCardProps & {kind?: 'book' | 'periodical' | 'newspaper'};

function Resume({read = false, kind = 'book', ...props}: ReadResumeCardProps & {read?: boolean}) {
  const t = useTheme();
  if (t.isTV && read) return null;
  const width = props.width ?? (t.isTV ? 560 : 296), height = props.height ?? (t.isTV ? 216 : 150), pad = t.isTV ? 20 : 14;
  const coverHeight = height - pad * 2, coverWidth = read ? coverHeight * (kind === 'book' ? 2 / 3 : 3 / 4) : coverHeight;
  return <CardFrame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered} holding={s.holding} height={height} />}>
    {s => <ArtFrame height={height} radius={14} focused={s.focused} hovered={s.hovered} holding={s.holding} hoverScale={1.02} selected={props.selection?.selected}>
      <AmbientArt source={props.ambient} palette={props.palette} treatment="cover" visible={props.visible} priority={props.priority} />
      <View style={{flex: 1, flexDirection: 'row', gap: 14, padding: pad, paddingRight: 16}}>
        <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind={read ? kind : 'square'} source={props.artwork} fallback={props.artworkFallback} width={coverWidth} height={coverHeight} style={{borderRadius: read ? 5 : 8}} />
        <View style={{flex: 1, minWidth: 0, paddingTop: 6}}>
          <Text numberOfLines={2} style={[titleStyle(t, t.isTV ? 26 : 16, 800, -0.01), {lineHeight: (t.isTV ? 31.2 : 19.2) * t.textScale, color: props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
          <View style={{marginTop: 3}}><Parts parts={props.caption} style={{color: t.colors.textSecondary}} /></View>
          <View style={{marginTop: 'auto', flexDirection: 'row', gap: 10, justifyContent: 'space-between'}}>{props.detail?.map(part => <View key={part.id} style={{flexShrink: part.id === 'remaining' ? 0 : 1, minWidth: 0}}><Parts parts={[part]} strong style={titleStyle(t, t.isTV ? 20 : 12.5, part.id === 'remaining' ? 600 : 700)} /></View>)}</View>
          <Progress value={props.progress} height={t.isTV ? 6 : 4} style={{marginTop: 7, marginBottom: 4}} />
        </View>
      </View>
    </ArtFrame>}
  </CardFrame>;
}
export function ResumeCard(props: ResumeCardProps) {return <Resume {...props} />;}
export function ReadResumeCard(props: ReadResumeCardProps) {return <Resume {...props} read />;}

export type QuickTileProps = CardProps & {artwork?: ArtworkSource | null; artworkFallback?: ReactNode; artworkProps?: Omit<ArtworkProps, 'source' | 'fallback' | 'width' | 'height'>; height?: number} & ({playing: true; onPause: () => void} | {playing?: false; onPause?: never});
export function QuickTile(props: QuickTileProps) {
  const t = useTheme(), width = props.width ?? (t.isTV ? 420 : 350), height = props.height ?? (t.isTV ? 112 : 76);
  return <CardFrame {...props} width={width} overlay={s => <CardOverlays props={props} hovered={s.hovered || !!props.playing} holding={s.holding} height={height} playSize={36} pause={props.playing ? props.onPause : undefined} />}>
    {s => <ArtFrame height={height} radius={10} focused={s.focused} hovered={s.hovered} holding={s.holding} hoverScale={1} selected={props.selection?.selected} style={{backgroundColor: props.playing ? t.colors.raisedHigh : t.colors.raised}}>
      <View style={{flexDirection: 'row', gap: 16, alignItems: 'center', flex: 1}}>
        <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="square" source={props.artwork} fallback={props.artworkFallback} width={height} height={height} />
        <View style={{flex: 1, minWidth: 0, paddingRight: 58}}>
          <Text numberOfLines={1} style={[titleStyle(t, t.isTV ? 24 : 15), {color: props.playing || props.selection?.selected ? t.colors.accentStrong : t.colors.text}]}>{props.title}</Text>
          <View style={{marginTop: 3, flexDirection: 'row', gap: 8, alignItems: 'center'}}>{props.playing && <View accessible={false} style={{height: 14, flexDirection: 'row', alignItems: 'flex-end', gap: 2}}>{[0.55, 1, 0.38, 0.75].map((fraction, index) => <View key={index} style={{height: 14 * fraction, width: 3, borderRadius: 1, backgroundColor: t.colors.accentStrong}} />)}</View>}<Parts parts={props.caption} /></View>
        </View>
      </View>
    </ArtFrame>}
  </CardFrame>;
}

/** Only already-authorized display facts enter this renderer; it performs no admin read. */
export type SessionCardProps = CardProps & {
  artwork?: ArtworkSource | null;
  artworkFallback?: ReactNode;
  artworkProps?: Omit<ArtworkProps, 'source' | 'fallback' | 'width' | 'height'>;
  identity: readonly FactPart[];
  facts: readonly FactPart[];
  ambient?: AmbientArtSource;
  palette?: ArtworkPalette;
  onStop?: () => void;
  progress?: number;
  paused?: boolean;
  height?: number;
  viewerBadge?: ReactNode;
  /** Supplied profile and optional genuine username; absent viewer retains legacy facts. */
  viewer?: SessionViewer;
} & ({kind?: 'video' | 'audio'; channelMark?: never; channelName?: never; channelNumber?: never} | {kind: 'channel'; channelMark: ChannelMarkModel; channelName: string; channelNumber?: string});
export function SessionCard(props: SessionCardProps) {
  const t = useTheme(), width = props.width ?? (t.isTV ? 640 : 358), height = props.height ?? (t.isTV ? 220 : 182), artWidth = t.isTV ? 176 : 124, artHeight = artWidth * 9 / 16;
  const who = props.viewer ? sessionViewerParts(props.viewer) : props.facts.filter(p => p.id === 'viewer' || p.id === 'account' || p.id === 'device');
  const state = props.facts.find(p => p.id === 'state');
  const position = props.facts.filter(p => p.id === 'position' || props.kind === 'channel' && p.id === 'started');
  const facts = props.facts.filter(p => !['viewer', 'account', 'device', 'state', 'position', 'started'].includes(p.id));
  return <CardFrame {...props} width={width} accessibilityDetails={[...(props.accessibilityDetails ?? []), ...props.identity.map(part => part.text), ...who.map(part => part.text), ...facts.map(part => part.text), ...position.map(part => part.text), props.paused ? state?.text ?? defaultI18n.t('next.kit.session.paused') : state?.text, ...(props.kind === 'channel' ? [props.channelName, props.channelNumber] : [])]} overlay={() => <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, top: 0, height}}>
    {!t.isTV && <View style={{position: 'absolute', right: 12, bottom: 14, flexDirection: 'row', gap: 8}}>
      {props.onStop && <Button variant="glass" size="xs" icon="i-stop" iconSize={14} disabled={props.disabled} onPress={props.onStop} style={{height: 38, paddingHorizontal: 14, borderRadius: 10}} textStyle={titleStyle(t, 14, 700)}>{defaultI18n.t('next.cards.stop')}</Button>}
      {props.onMore && <Button variant="glass" size="xs" icon="i-more" iconSize={18} iconOnly disabled={props.disabled} accessibilityLabel={defaultI18n.t('card.more', {title: props.title})} onPress={props.onMore} style={{height: 38, borderRadius: 10}} />}
    </View>}
  </View>}>
    {s => <ArtFrame height={height} radius={14} focused={s.focused} holding={s.holding} selected={props.selection?.selected}>
      <View style={{padding: 12, flex: 1}}>
      <View style={{flexDirection: 'row', gap: 14, alignItems: 'flex-start'}}>
        <View style={{width: artWidth, height: artHeight, borderRadius: 9, overflow: 'hidden', alignSelf: 'flex-start'}}>
          {props.kind === 'audio' ? <>
            <AmbientArt source={props.ambient} palette={props.palette} treatment="cover" visible={props.visible} priority={props.priority} />
            <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="square" source={props.artwork} fallback={props.artworkFallback} width={52} height={52} style={{position: 'absolute', left: (artWidth - 52) / 2, top: (artHeight - 52) / 2, borderRadius: 5}} />
          </> : props.kind === 'channel' && props.channelMark ? <>
            <AmbientArt palette={props.palette} treatment="cover" visible={props.visible} priority={props.priority} />
            <View style={{paddingVertical: 8, paddingHorizontal: 9, flex: 1, justifyContent: 'space-between'}}><ChannelMark mark={props.channelMark} source={props.artwork ?? undefined} size={26} visible={props.visible} />
              <View style={{flexDirection: 'row', gap: 6}}><Text numberOfLines={1} style={[titleStyle(t, 10.5, 700), {color: t.colors.textSecondary, flexShrink: 1}]}>{props.channelName}</Text>{props.channelNumber && <Text style={[titleStyle(t, 10.5, 700), {color: t.colors.textSecondary}]}>{props.channelNumber}</Text>}</View>
            </View>
          </> : <MediaArt {...props.artworkProps} visible={props.visible ?? props.artworkProps?.visible} priority={props.priority ?? props.artworkProps?.priority} kind="wide" source={props.artwork} fallback={props.artworkFallback} width={artWidth} height={artHeight} />}
        </View>
        <View style={{flex: 1, minWidth: 0}}>
          <Text numberOfLines={1} style={[titleStyle(t, t.isTV ? 24 : 16, 700, -0.01), { color: t.colors.text, paddingRight: props.paused ? 66 : 0}]}>{props.title}</Text>
          <View style={{marginTop: 2}}><Parts parts={props.identity} style={{color: t.colors.textSecondary}} /></View>
          <View style={{marginTop: 9, flexDirection: 'row', alignItems: 'center', gap: 7}}>{props.viewerBadge}<Parts parts={who} /></View>
        </View>
      </View>
      <View style={{marginTop: 12, flexDirection: 'row', gap: 6, flexWrap: 'wrap'}}>{facts.map(p => <View key={p.id} style={{paddingVertical: 3, paddingHorizontal: 8, borderRadius: 6, backgroundColor: alpha(p.role === 'status' ? t.colors.accent : t.colors.text, p.role === 'status' ? 0.16 : 0.07)}}><Text style={[titleStyle(t, 12, 700), {color: p.role === 'status' ? t.colors.accentStrong : t.colors.textSecondary}]}>{p.text}</Text></View>)}</View>
      <View style={{marginTop: 'auto', minHeight: 38, justifyContent: 'center', paddingRight: !t.isTV && (props.onStop || props.onMore) ? 130 : 0}}><Parts parts={position} style={{color: t.colors.textSecondary}} /></View>
      </View>
      {props.paused && <Text style={[titleStyle(t, 12, 700), {position: 'absolute', right: 12, top: 12, color: t.colors.textSecondary}]}>{state?.text ?? defaultI18n.t('next.kit.session.paused')}</Text>}
      <Progress value={props.progress} height={3} paused={props.paused} style={{position: 'absolute', left: 0, right: 0, bottom: 0}} />
    </ArtFrame>}
  </CardFrame>;
}
