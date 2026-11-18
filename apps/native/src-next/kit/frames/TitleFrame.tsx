import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {Button} from '../controls/Button';
import {Tabs, type TabItem} from '../controls/Tabs';
import type {IconName} from '../Icon';
import {Lockup} from '../layout/Lockup';
import {RoundBarButton, type TopBarBack} from '../layout/TopBar';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {PageStateFrame, type PageStateProps} from './PageStateFrame';

export type TitleAction = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; testID?: string}>;
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
  primary: TitleAction;
  /** Round buttons after it, in fixed slots from the left of the primary's end. One that the title doesn't support
   * stays in its slot with `disabled`. */
  actions?: readonly TitleAction[];
  /** Flexible: collapses when absent. */
  synopsis?: string;
  tabs?: Readonly<{items: readonly TabItem[]; value: string; onValueChange: (value: string) => void}>;
  /** The tab panel: the one flexible region. */
  children?: ReactNode;
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
  testID?: string;
}>;

/** Frame 2, a title page: a film, show, person, album or book. Fixed: the art box, Back, the title slot (the title
 * sits on its bottom edge and grows upward over the art), the metadata line, the action row and the tabs bar.
 * Flexible: the synopsis and the tab panel under them. Loading draws the same boxes. */
export function TitleFrame({art, back, topAction, title, metadata, primary, actions = [], synopsis, tabs, children, state, testID}: TitleFrameProps) {
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
  const words = synopsis !== undefined && !loading && <Text numberOfLines={spec.synopsisLines[form]} style={[theme.type.intro, {color: theme.colors.textSecondary, maxWidth: frameSpec.settings.paragraphMax}]}>{synopsis}</Text>;
  const round = (action: TitleAction) => <Button key={action.label} variant="glass" size="hero" iconOnly icon={action.icon} accessibilityLabel={action.label} disabled={action.disabled || loading} onPress={action.onPress} testID={action.testID}
    style={{width: actionHeight, height: actionHeight}}/>;
  const row = <View style={{height: actionHeight, flexDirection: 'row', alignItems: 'center', gap: theme.spec.hero.actions.gap[theme.platform]}}>
    <Button variant="primary" size="hero" icon={primary.icon} disabled={primary.disabled || loading} onPress={primary.onPress} testID={primary.testID} preferredFocus align={stacked ? 'stretch' : 'start'} style={stacked ? {flex: 1} : undefined}>{primary.label}</Button>
    {actions.map(round)}
  </View>;
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
    {stacked && <View style={{paddingHorizontal: frame.gutter, marginTop: spec.actionGap[form], gap: theme.space.sm}}>{row}{words}</View>}
    {tabs && <Tabs items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} disabled={loading} style={{marginTop: stacked ? theme.space.sm : 0}} contentContainerStyle={{paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}/>}
    <View style={{marginTop: frame.headerGap + frame.headingGap, gap: frame.sectionGap}}>{loading ? null : children}</View>
  </ScrollView>;
}
