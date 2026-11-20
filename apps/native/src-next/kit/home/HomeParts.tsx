import React, {type ReactNode} from 'react';
import {View, type ViewProps} from 'react-native';
import {AmbientArt, type AmbientArtSource, type ArtworkSource} from '../artwork';
import {Button} from '../controls';
import type {ArtworkRenderContext} from '../layout/types';
import type {IconName} from '../Icon';
import {LoadingPaint, SkeletonBlock} from '../states';
import {useTheme} from '../theme';

export type HomeHeroAction = Readonly<{id: string; label: string; icon: IconName; variant: 'primary' | 'glass'; onPress: () => void}>;

/** The spotlight's buttons on a phone (phone-app.html 1: Play, My List), side by side at the hero's action gap. */
export function HomeHeroActions({actions}: Readonly<{actions: readonly HomeHeroAction[]}>) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.spec.hero.actions.gap[theme.column]}}>
    {actions.map(action => <Button key={action.id} icon={action.icon} variant={action.variant} size="hero" onPress={action.onPress}>{action.label}</Button>)}
  </View>;
}

/** One Home card's box: its drawn size (it may grow under larger text), holding the card's own skeleton while the row's
 * page is still arriving. */
export function HomeCardSlot({width, height, waiting, children}: Readonly<{width: number; height: number; waiting: boolean; children: ReactNode}>) {
  const theme = useTheme(), grows = theme.fontScale > 1 || theme.textScale > 1;
  return <View style={grows ? {width, minHeight: height} : {width, height}}>
    <LoadingPaint ready={!waiting} immediate={!waiting} skeleton={<View style={{backgroundColor: theme.colors.ink}}><SkeletonBlock width={width} height={height}/></View>}>{children}</LoadingPaint>
  </View>;
}

/** A collection row on a phone: the banner inside the page's gutters, as wide as the room between them. */
export function HomeCollectionRow({width, children}: Readonly<{width: number; children: (bannerWidth: number) => ReactNode}>) {
  const theme = useTheme();
  return <View style={{paddingHorizontal: theme.gutter}}>{children(width - theme.gutter * 2)}</View>;
}

/** Where one Home row sits: a mounted row reports its measured box (the page's row window reads it); a row outside the
 * window keeps only its height, so the page scrolls the same distance without drawing it. */
export function HomeRowPlace({height, onLayout, children}: Readonly<{height?: number; onLayout?: ViewProps['onLayout']; children?: ReactNode}>) {
  return height === undefined ? <View onLayout={onLayout}>{children}</View> : <View style={{height}}/>;
}

/** The spotlight's picture on a phone: the slide's backdrop over its own ambient wash, faded in as the hero tells it. */
export function HomeHeroArt({ambient, picture, context, width, height, visible}: Readonly<{
  ambient?: AmbientArtSource; picture?: ArtworkSource; context: ArtworkRenderContext; width: number; height: number; visible: boolean;
}>) {
  return <AmbientArt source={ambient} foreground={picture} treatment="phone" visible={visible && context.visible} priority
    onReady={context.onReady} onFallbackReady={context.onReady} onError={context.onError} foregroundStyle={context.foregroundStyle} width={width} height={height}/>;
}
