import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {Button, ResumeButton} from '../controls/Button';
import {Tabs, type TabItem} from '../controls/Tabs';
import type {IconName} from '../Icon';
import {Lockup} from '../layout/Lockup';
import {RoundBarButton, type TopBarBack} from '../layout/TopBar';
import {alpha} from '../shell/visual';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';

export type TitleAction = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; testID?: string;
  /** Shown with its label where there is room (a tablet and TV): Start over, My List. A round button on a phone. */
  labelled?: boolean}>;
/** The one flexible button. With `progress` it is Resume. On a tablet and TV the time left is beside its label, the
 * played part runs along its lower edge and Start over is the button after it (detail-desktop 2, detail-tv 2). On a
 * phone the played part, the time left and Start over are one line under the action row (detail-phone 3). */
export type TitlePrimary = Omit<TitleAction, 'labelled'> & Readonly<{progress?: number; remaining?: string; startOver?: Readonly<{label: string; onPress: () => void}>}>;
/** One part of the credits line: "Directed by" and the names. */
export type TitleCredit = Readonly<{label: string; names: string}>;
export type TitleFrameProps = Readonly<{
  /** The picture, as the kit's `AmbientArt` with `treatment="title"`; it fades itself into the backdrop, and with no
   * picture it draws the generated wash. The frame gives it the box. */
  art: ReactNode;
  /** Not drawn on TV, where the remote's Back leaves the page. */
  back: TopBarBack;
  /** One round glass button opposite Back (Play on). */
  topAction?: TitleAction;
  title: string;
  /** The metadata line (`HeroMetadata`). Its line is kept when there is nothing to show. */
  metadata?: ReactNode;
  /** The one flexible button: it takes the room the round buttons leave. */
  primary: TitlePrimary;
  /** Round buttons after it, in fixed slots from the left of the primary's end. One that the title doesn't support
   * stays in its slot with `disabled`. */
  actions?: readonly TitleAction[];
  /** One bold line over the synopsis. Flexible: collapses when absent. */
  tagline?: string;
  /** Flexible: collapses when absent. */
  synopsis?: string;
  /** "Directed by" and "Starring", one line under the synopsis on a tablet and TV. Flexible: collapses when absent. */
  credits?: readonly TitleCredit[];
  tabs?: Readonly<{items: readonly TabItem[]; value: string; onValueChange: (value: string) => void}>;
  /** The tab panel: the one flexible region. */
  children?: ReactNode;
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
  testID?: string;
}>;

/** Frame 2, a title page: a film, show, person, album or book. Fixed: the art box, Back, the title slot (the title
 * sits on its bottom edge and grows upward over the art), the metadata line, the action row and the tabs bar.
 * Flexible: the synopsis and the tab panel under them. Loading draws the same boxes. */
export function TitleFrame({art, back, topAction, title, metadata, primary, actions = [], tagline, synopsis, credits, tabs, children, state, testID}: TitleFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), spec = frameSpec.hero.title;
  const {form} = frame, tv = form === 'tv', stacked = form === 'phone';
  const failure = state && 'title' in state ? state : undefined, loading = !!state && !failure;
  if (failure) return <View testID={testID} style={{flex: 1}}>
    {!tv && <View style={{position: 'absolute', zIndex: theme.layers.sticky, left: theme.space.sm, top: insets.top + theme.space.tight}}><RoundBarButton icon="i-left" label={back.label} onPress={back.onPress} testID={back.testID}/></View>}
    <PageStateFrame {...failure}/>
  </View>;
  const height = spec.height[form], lockup = spec.lockup[form], actionHeight = theme.spec.button.hero.height[theme.platform];
  const identity = <>
    {loading ? <SkeletonBlock width="70%" height={lockup}/> : <Lockup title={title} size={lockup} lineRatio={frameSpec.hero.lockupLine} tracking={frameSpec.hero.lockupTracking} lines={tv ? 2 : undefined} style={{textAlign: stacked ? 'center' : 'left'}}/>}
    <View style={{minHeight: theme.type.rowLabel.lineHeight, marginTop: theme.space.controlGap, alignItems: stacked ? 'center' : 'flex-start', justifyContent: 'center'}}>{loading ? <SkeletonBlock width="40%" height={theme.type.meta.fontSize}/> : metadata}</View>
  </>;
  const words = !loading && (tagline !== undefined || synopsis !== undefined || credits?.length) ? <View style={{gap: spec.creditsGap}}>
    {tagline !== undefined && <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{tagline}</Text>}
    {synopsis !== undefined && <Text numberOfLines={spec.synopsisLines[form]} style={[theme.type.intro, {color: theme.colors.textSecondary, maxWidth: frameSpec.settings.paragraphMax}]}>{synopsis}</Text>}
    {!stacked && credits && credits.length > 0 && <Text numberOfLines={1} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>
      {credits.map((credit, index) => <Text key={credit.label}>{index > 0 ? '    ' : ''}{credit.label} <Text style={{color: theme.colors.textSecondary}}>{credit.names}</Text></Text>)}</Text>}
  </View> : null;
  const side = (action: TitleAction) => action.labelled && !stacked
    ? <Button key={action.label} variant="glass" size="hero" icon={action.icon} disabled={action.disabled || loading} onPress={action.onPress} testID={action.testID}>{action.label}</Button>
    : <Button key={action.label} variant="glass" size="hero" iconOnly icon={action.icon} accessibilityLabel={action.label} disabled={action.disabled || loading} onPress={action.onPress} testID={action.testID}
      style={{width: actionHeight, height: actionHeight}}/>;
  const lead = {icon: primary.icon, disabled: primary.disabled || loading, onPress: primary.onPress, testID: primary.testID, preferredFocus: true, align: stacked ? 'stretch' as const : 'start' as const, style: stacked ? {flex: 1} : undefined};
  const row = <View style={{height: actionHeight, flexDirection: 'row', alignItems: 'center', gap: theme.spec.hero.actions.gap[theme.platform]}}>
    {primary.progress !== undefined && !stacked ? <ResumeButton {...lead} size="hero" progress={primary.progress} remaining={primary.remaining}>{primary.label}</ResumeButton> : <Button {...lead} variant="primary" size="hero">{primary.label}</Button>}
    {primary.startOver && !stacked && <Button variant="glass" size="hero" icon="i-restart" disabled={loading} onPress={primary.startOver.onPress}>{primary.startOver.label}</Button>}
    {actions.map(side)}
  </View>;
  const track = theme.spec.card.progress.height[theme.platform];
  const resumeLine = primary.progress !== undefined && stacked && !loading ? <View style={{gap: theme.space.tight}}>
    <View accessibilityRole="progressbar" accessibilityValue={{min: 0, max: 1, now: primary.progress}} style={{height: track, borderRadius: track, overflow: 'hidden', backgroundColor: alpha(theme.colors.text, theme.spec.card.progress.trackAlpha)}}>
      <View style={{width: `${primary.progress * 100}%`, height: track, backgroundColor: theme.colors.accentStrong}}/></View>
    <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}><Text style={[theme.type.rowHelp, {color: theme.colors.text}]}>{primary.remaining}</Text>
      {primary.startOver && <Button variant="text" size="xs" icon="i-restart" onPress={primary.startOver.onPress}>{primary.startOver.label}</Button>}</View>
  </View> : null;
  return <ScrollView testID={testID} style={{flex: 1}} contentContainerStyle={{paddingBottom: frame.bottomClear}}>
    <View style={{height}}>
      {art}
      {!tv && <View pointerEvents="box-none" style={{position: 'absolute', zIndex: theme.layers.sticky, left: theme.space.sm, right: theme.space.sm, top: insets.top + theme.space.tight, flexDirection: 'row', justifyContent: 'space-between'}}>
        <RoundBarButton icon="i-left" label={back.label} onPress={back.onPress} testID={back.testID}/>
        {topAction && <RoundBarButton icon={topAction.icon} label={topAction.label} onPress={topAction.onPress} disabled={topAction.disabled} testID={topAction.testID}/>}
      </View>}
      {/* Everything in the art box hangs from its bottom edge, so a longer title grows upward and the row below never moves. */}
      <View pointerEvents="box-none" style={{position: 'absolute', left: frame.gutter, right: stacked ? frame.gutter : undefined, width: stacked ? undefined : theme.spec.hero.copy.titleWidth[tv ? 'tv' : 'web'], bottom: spec.copyBottom[form]}}>
        {identity}
        {!stacked && <><View style={{marginTop: theme.space.sm}}>{words}</View><View style={{marginTop: spec.actionGap[form]}}>{row}</View></>}
      </View>
    </View>
    {stacked && <View style={{paddingHorizontal: frame.gutter, marginTop: spec.actionGap[form], gap: theme.space.sm}}>{row}{resumeLine}{words}</View>}
    {tabs && <Tabs items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} disabled={loading} style={{marginTop: stacked ? theme.space.sm : 0}} contentContainerStyle={{paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}/>}
    <View style={{marginTop: frame.headerGap + frame.headingGap, gap: frame.sectionGap}}>{loading ? null : children}</View>
  </ScrollView>;
}
