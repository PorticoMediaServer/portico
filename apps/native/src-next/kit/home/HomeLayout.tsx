import React, {useEffect, useMemo, useRef, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Gesture, GestureDetector} from 'react-native-gesture-handler';
import type {I18n} from '../../../../../packages/i18n/src';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {alpha} from '../browse/presentation';
import {useTheme} from '../theme';
import type {NativeHomeCustomizeController} from '../../screens/home/customize-controller';
import {createHomeRowDrag, type HomeDragRow} from '../../screens/home/customize-drag';

/** A pick-up lands on the row itself (touch: a long press and drag on the handle). */
export function HomeDragHandle({controller, id, disabled, geometry, scrollGesture, testID}: {
  controller: NativeHomeCustomizeController; id: string; disabled: boolean;
  geometry: () => readonly HomeDragRow[]; scrollGesture: ReturnType<typeof Gesture.Native>; testID: string;
}) {
  const theme = useTheme(), latest = useRef(geometry);
  latest.current = geometry;
  const drag = useMemo(() => createHomeRowDrag(controller, id, () => latest.current()), [controller, id]);
  useEffect(() => () => drag.finish(false), [drag]);
  const pan = useMemo(() => Gesture.Pan().enabled(!disabled).activateAfterLongPress(200).blocksExternalGesture(scrollGesture).activeOffsetY([-2, 2]).runOnJS(true)
    .onStart(() => {drag.begin();}).onUpdate(event => {drag.update(event.translationY);})
    .onFinalize((_event, success) => {drag.finish(success);}), [disabled, drag, scrollGesture]);
  return <GestureDetector gesture={pan}><View testID={testID} accessible={false} collapsable={false}
    style={{width: 44, minHeight: 50, alignItems: 'center', justifyContent: 'center'}}>
    <Icon name="i-grip" size={20} color={theme.colors.textTertiary}/>
  </View></GestureDetector>;
}

export function HomeSavedViewChoices({views, hasMore, i18n, onChoose, firstRef, summary, disabled}: {disabled?: boolean; views: readonly PersonalResource[]; hasMore?: boolean; summary: (view: PersonalResource) => string | undefined; firstRef?: React.RefObject<View | null>; i18n: Pick<I18n, 't'>; onChoose: (view: PersonalResource) => void}) {
  const theme = useTheme();
  const eligible = views.filter(view => view.kind === 'view' && view.status === 'ready' && !view.onHome && view.actions.includes('pin'));
  return <ScrollView>{eligible.map((view, index) => {const detail = summary(view); return <Focusable ref={index === 0 ? firstRef : undefined} key={view.id} focusId={view.id} focusKind="row" disabled={disabled} onPress={() => onChoose(view)} style={{minHeight: theme.isTV ? 92 : undefined, paddingVertical: theme.isTV ? 12 : 20, paddingHorizontal: theme.isTV ? 20 : 0, flexDirection: 'row', alignItems: 'center', gap: 18}}>
      <View style={{width: theme.isTV ? 52 : 36, height: theme.isTV ? 52 : 36, borderRadius: theme.isTV ? 13 : 9, alignItems: 'center', justifyContent: 'center', backgroundColor: `${theme.colors.text}1A`}}><Icon name="i-filter" size={theme.isTV ? 28 : 20} color={theme.colors.textSecondary}/></View><View style={{flex: 1}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{view.name}</Text>
      {!!detail && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{detail}</Text>}</View>
    </Focusable>;})}{!eligible.length && !hasMore && <View style={{paddingVertical: 30, alignItems: 'center', gap: 8}}>
      <View style={{width: theme.isTV ? 52 : 44, height: theme.isTV ? 52 : 44, borderRadius: theme.isTV ? 14 : 11, alignItems: 'center', justifyContent: 'center', backgroundColor: `${theme.colors.text}1A`}}><Icon name="i-filter" size={theme.isTV ? 26 : 20} color={theme.colors.textSecondary}/></View>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{i18n.t('next.home.noViewsTitle')}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary, textAlign: 'center'}]}>{i18n.t('next.home.noViewsBody')}</Text>
    </View>}</ScrollView>;
}

/** collections 16 (`.crg`): Home's rows in one raised card, each a row with its handle, its name and what it shows, and its
 * switch or "Always on Home". The drop line is drawn in the same card, where the rows' measured places are. */
export function HomeLayoutCard({children, dropAt}: {children: ReactNode; /** Where a lifted row would land, from the card's top. */ dropAt?: number}) {
  const theme = useTheme();
  return <View style={{marginHorizontal: theme.isTV ? 0 : theme.space.xs, borderRadius: 14, backgroundColor: theme.colors.raisedHigh}}>
    {children}
    {dropAt !== undefined && <View pointerEvents="none" style={{position: 'absolute', zIndex: 5, left: 4, right: 8, top: dropAt - 2, height: 4, borderRadius: 2,
      backgroundColor: theme.colors.accentStrong, boxShadow: [{offsetX: 0, offsetY: 0, blurRadius: 16, color: alpha(theme.colors.accentStrong, .6)}]}}>
      <View style={{position: 'absolute', left: -6, top: -4, width: 12, height: 12, borderRadius: 6, backgroundColor: theme.colors.accentStrong}}/>
    </View>}
  </View>;
}

/** One Home row in the card (`.crp`): min 54, a hairline from 44 between rows; lifted, it rides over the others with the accent
 * outline and its shadow while its slot stays dashed. */
export function HomeLayoutRow({first, lifted, shift, height, onMeasure, handle, leading, title, detail, hidden, trailing, rowRef, testID, disabled,
  accessibilityLabel, accessibilityActions, onAccessibilityAction}: {
  first: boolean; lifted: boolean; shift: number; height?: number; onMeasure: (y: number, height: number) => void; handle?: ReactNode; leading?: ReactNode;
  title: string; detail?: string; hidden: boolean; trailing?: ReactNode; rowRef: (node: View | null) => void; testID: string; disabled: boolean;
  accessibilityLabel: string; accessibilityActions: React.ComponentProps<typeof Focusable>['accessibilityActions'];
  onAccessibilityAction: React.ComponentProps<typeof Focusable>['onAccessibilityAction'];
}) {
  const theme = useTheme(), min = 54;
  return <View onLayout={event => onMeasure(event.nativeEvent.layout.y, event.nativeEvent.layout.height)}
    style={{minHeight: min, height: lifted ? height : undefined, zIndex: lifted ? 8 : undefined,
      ...(lifted ? {borderWidth: 2, borderStyle: 'dashed', borderRadius: 14, borderColor: alpha(theme.colors.lineStrong, .6)} : {})}}>
    {!first && !lifted && <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 44, right: 0, height: 1, backgroundColor: theme.colors.lineSoft}}/>}
    <View style={{flexDirection: 'row', alignItems: 'center', minHeight: min, gap: theme.space.controlGap, paddingRight: theme.space.sm - 2,
      ...(lifted ? {position: 'absolute', left: 0, right: 0, backgroundColor: theme.colors.raisedBright, borderRadius: 14,
        transform: [{translateY: shift}, {scale: 1.02}], boxShadow: [{offsetX: 0, offsetY: 24, blurRadius: 50, spreadDistance: -16, color: alpha(theme.colors.shadow, .9)},
          {offsetX: 0, offsetY: 0, blurRadius: 0, spreadDistance: 2, color: theme.colors.accent}]} : {})}}>
      {handle ?? <View style={{width: 36}}/>}
      {leading}
      <Focusable ref={rowRef} testID={testID} disabled={disabled} focusKind="row" focusRadius={16} accessibilityLabel={accessibilityLabel}
        accessibilityState={{selected: lifted}} accessibilityActions={accessibilityActions} onAccessibilityAction={onAccessibilityAction}
        style={{flex: 1, minHeight: min, justifyContent: 'center', paddingVertical: theme.space.xs}}>
        <Text style={[theme.type.rowLabel, {color: hidden ? theme.colors.textTertiary : theme.colors.text}]}>{title}</Text>
        {!!detail && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2}]}>{detail}</Text>}
      </Focusable>
      {/* The kit Switch sits at the start of its own line; here it is centred on the row like everything else. */}
      {trailing !== undefined && <View style={{alignSelf: 'stretch', justifyContent: 'center'}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>{trailing}</View>
      </View>}
    </View>
  </View>;
}

/** A row Home always shows: the lock and "Always on Home" where a switch would be. */
export function HomeAlwaysOn({label}: {label: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
    <Icon name="i-lock" size={theme.isTV ? 18 : 14} color={theme.colors.textTertiary}/>
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{label}</Text>
  </View>;
}

/** A saved view's row mark: the filter glyph on a faint tile. */
export function HomeViewMark() {
  const theme = useTheme();
  return <View style={{width: 36, height: 36, borderRadius: 10, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .1)}}>
    <Icon name="i-filter" size={22} color={theme.colors.textSecondary}/>
  </View>;
}

/** The sheet's scrolling body: it holds still while a row is lifted, so a drag moves the row, not the list. */
export function HomeLayoutScroll({locked, scrollRef, gesture, children}: {locked: boolean; scrollRef?: React.Ref<ScrollView>;
  /** The list's own native scroll gesture, which a row's drag handle blocks while it moves the row. */ gesture: ReturnType<typeof Gesture.Native>; children: ReactNode}) {
  const theme = useTheme();
  return <GestureDetector gesture={gesture}><ScrollView ref={scrollRef} removeClippedSubviews={false} scrollEnabled={!locked} keyboardShouldPersistTaps="handled"
    contentContainerStyle={{paddingTop: theme.space.xs, paddingBottom: theme.space.md, gap: theme.space.sm}}>{children}</ScrollView></GestureDetector>;
}
