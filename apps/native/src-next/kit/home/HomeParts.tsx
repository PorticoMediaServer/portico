import React, {type ReactNode} from 'react';
import {View, type ViewProps} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {Hero, type HeroCandidate} from '../layout';
import type {HeroProps} from '../layout/Hero';
import {AmbientArt, type AmbientArtSource, type ArtworkSource} from '../artwork';
import {Button} from '../controls';
import {FocusRegion, type FocusRegionProps} from '../focus';
import type {ArtworkRenderContext} from '../layout/types';
import type {IconName} from '../Icon';
import {LoadingPaint, SkeletonBlock} from '../states';
import {useTheme} from '../theme';
import {roundBarSize} from '../layout/TopBar';

export type HomeHeroAction = Readonly<{id: string; label: string; icon?: IconName; variant: 'primary' | 'glass'; onPress: () => void;
  onFocus?: () => void; onBlur?: () => void; onLongPress?: () => void}>;

/** The spotlight's buttons on a phone (phone-app.html 1: Play, My List), side by side at the hero's action gap. */
export function HomeHeroActions({actions, focus}: Readonly<{actions: readonly HomeHeroAction[]; focus?: FocusRegionProps}>) {
  const theme = useTheme();
  const row = {flexDirection: 'row', alignItems: 'center', gap: theme.spec.hero.actions.gap[theme.column]} as const;
  const buttons = actions.map(action => <Button key={action.id} focusId={action.id} icon={action.icon} variant={action.variant} size="hero"
    onPress={action.onPress} onFocus={action.onFocus} onBlur={action.onBlur} onLongPressAction={action.onLongPress}>{action.label}</Button>);
  return focus ? <FocusRegion {...focus} style={row}>{buttons}</FocusRegion> : <View style={row}>{buttons}</View>;
}

/** One Home card's box: its drawn size (it may grow under larger text), holding the card's own skeleton while the row's
 * page is still arriving. */
export function HomeCardSlot({width, height, waiting, grow, children}: Readonly<{width: number; height: number; waiting: boolean; grow?: boolean; children: ReactNode}>) {
  const theme = useTheme(), grows = grow || theme.fontScale > 1 || theme.textScale > 1;
  return <View style={grows ? {width, minHeight: height} : {width, height}}>
    <LoadingPaint ready={!waiting} immediate={!waiting} skeleton={<View style={{backgroundColor: theme.colors.ink}}><SkeletonBlock width={width} height={height}/></View>}>{children}</LoadingPaint>
  </View>;
}

/** A collection row on a phone: the banner inside the page's gutters, as wide as the room between them. */
export function HomeCollectionRow({width, children}: Readonly<{width: number; children: (bannerWidth: number) => ReactNode}>) {
  const theme = useTheme();
  const right = theme.isTV ? theme.layout.gutter.tvRight : theme.gutter;
  return <View style={{paddingLeft: theme.gutter, paddingRight: right}}>{children(width - theme.gutter - right)}</View>;
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
  const theme = useTheme(), wide = theme.form === 'expanded', tablet = theme.spec.hero.tabletHome;
  return <AmbientArt source={ambient} foreground={picture} treatment={wide || theme.isTV ? 'hero' : 'phone'}
    foregroundFraction={wide ? tablet.landscapeArtwork : undefined} foregroundFade={wide ? 'left' : undefined}
    position={theme.form === 'medium' ? tablet.portraitArtwork : undefined} visible={visible && context.visible} priority
    onReady={context.onReady} onFallbackReady={context.onReady} onError={context.onError} foregroundStyle={context.foregroundStyle} width={width} height={height}/>;
}

export function HomeSpotlight<T extends HeroCandidate>({toolbar, ...props}: HeroProps<T> & {toolbar?: ReactNode}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), wide = theme.form === 'expanded';
  const showHero = props.candidates.length > 0;
  if (theme.form === 'phone') return showHero ? <Hero {...props} variant="phone-app"/> : null;
  const top = Math.max(insets.top, theme.space.md) + theme.space.controlGap;
  const spec = theme.spec.hero.tabletHome;
  return <View style={{height: showHero ? props.height : toolbar ? top + roundBarSize : 0}}>
    {showHero && <Hero {...props} variant={wide ? 'desktop' : 'phone-app'}
      copySide={wide ? spec.landscapeCopy : undefined}
      copyStyle={wide ? undefined : spec.portraitCopy}
      longLockupStyle={wide ? theme.type.pageTitle : undefined}
      metadataStyle={{marginBottom: wide ? theme.space.sm : theme.space.md}}
      thumbnailSize={wide ? spec.thumbnails : undefined}/>}
    {toolbar && <View pointerEvents="box-none" style={{position: 'absolute', top, right: theme.gutter}}>{toolbar}</View>}
  </View>;
}
