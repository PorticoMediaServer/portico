import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable, Switch, TextField} from '../controls';
import {Icon} from '../Icon';
import {BottomSheet} from '../overlays';
import {useTheme} from '../theme';

/** One season (or one group of episodes) in the sheet: its name, its episode count, and how much of it is watched. */
export type SeasonSheetRow = Readonly<{id: string; label: string; count?: string; number?: number;
  /** "Watched" or "9 of 24 watched"; `fraction` draws the meter, `complete` the tick. */
  progress?: string; fraction?: number; complete?: boolean}>;
export type SeasonSheetProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rows: readonly SeasonSheetRow[];
  /** How many seasons the show has (the heading's count, and whether Jump to season is offered: more than 30). */
  total: number;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onMore?: () => void;
  hideWatched: boolean;
  onHideWatched: (value: boolean) => void;
  returnFocusRef?: React.RefObject<View | null>;
}>;

/** phone-detail d2 frame 5: the season sheet. Jump to season leads on a long show, then each season with the server's
 * own count and the viewer's watched progress. Moved from `screens/watch/SeasonsSheet.tsx`, which maps the show's
 * seasons and groups to rows; the title page and the watch page's Episodes panel draw this same part. */
export function SeasonSheet(props: SeasonSheetProps) {
  const theme = useTheme(), t = defaultI18n.t, [jump, setJump] = useState(''), viewport = useWindowDimensions();
  const scroll = useRef<ScrollView>(null), first = useRef<View>(null);
  const initial = props.rows.find(row => row.id === props.selectedId)?.id ?? props.rows[0]?.id;
  useEffect(() => {if (props.open) setJump('');}, [props.open]);
  const canJump = props.total > 30;
  const go = () => {
    const number = Number.parseInt(jump, 10);
    if (!Number.isSafeInteger(number) || number < 1 || number > props.total) return;
    const season = props.rows.find(value => value.number === number);
    if (season) {setJump(''); props.onSelect(season.id); props.onOpenChange(false);}
  };
  return <BottomSheet testID="watch-seasons" open={props.open} onOpenChange={props.onOpenChange} title={t('title.seasons')} showClose={false}
    initialFocusRef={first} returnFocusRef={props.returnFocusRef}
    headerContent={<View style={{flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 20, paddingBottom: 10}}>
      <Text accessibilityRole="header" style={{color: theme.colors.text, fontSize: 21, fontWeight: '800', letterSpacing: -0.4}}>{t('title.seasons')}</Text>
      <Text style={{color: theme.colors.textTertiary, fontSize: 14, fontWeight: '600'}}>{defaultI18n.number(props.total, {useGrouping: false})}</Text>
      <View style={{flex: 1}}/>
      <Button ref={!initial ? first : undefined} focusId="watch-seasons-done" testID="watch-seasons-done" variant="text" onPress={() => props.onOpenChange(false)}>{t('action.done')}</Button>
    </View>}
    contentStyle={{paddingHorizontal: 0, flex: 1}} detents={[Math.round(viewport.height * .72)]}>
    <View style={{flex: 1}}>
      {canJump && <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: 8, paddingHorizontal: 16, marginBottom: 8}}>
        <View style={{flex: 1}}><TextField label={t('title.jumpToSeason')} value={jump} onChangeText={setJump} keyboardType="number-pad"
          description={t('title.jumpToHint', {min: '1', max: String(props.total)})} onSubmitEditing={go} returnKeyType="go" testID="watch-seasons-jump"/>
        </View>
        <Button focusId="watch-seasons-go" testID="watch-seasons-go" variant="glass" size="small" onPress={go}>{t('title.go')}</Button>
      </View>}
      <View style={{marginHorizontal: 16, marginBottom: 6}}>
        <Switch variant="compact" checked={props.hideWatched} onCheckedChange={props.onHideWatched} testID="watch-seasons-hide-watched">{t('next.controls.hideWatched')}</Switch>
      </View>
      <ScrollView ref={scroll} onScroll={event => {const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
        if (props.onMore && contentSize.height - contentOffset.y - layoutMeasurement.height < layoutMeasurement.height) props.onMore();}} scrollEventThrottle={64}>
        {props.rows.map(row => {
          const selected = row.id === props.selectedId;
          return <Focusable key={row.id} ref={row.id === initial ? first : undefined} focusId={`watch-season-${row.id}`} testID={`watch-season-${row.id}`} focusKind="row" focusRadius={0} accessibilityRole="button"
            accessibilityState={{selected}} onPress={() => {props.onSelect(row.id); props.onOpenChange(false);}}
            style={{position: 'relative', minHeight: 58, flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 20,
              backgroundColor: selected ? '#409fd424' : 'transparent', borderTopWidth: 1, borderTopColor: selected ? 'transparent' : theme.colors.line}}>
            {selected && <View style={{position: 'absolute', left: 0, top: 10, bottom: 10, width: 3, borderTopRightRadius: 3, borderBottomRightRadius: 3, backgroundColor: theme.colors.accent}}/>}
            <View style={{flex: 1, minWidth: 0}}>
              <Text style={{color: theme.colors.text, fontSize: 16, fontWeight: '700'}}>{row.label}</Text>
              {row.count ? <Text style={{color: theme.colors.textTertiary, fontSize: 12.5, fontWeight: '600', marginTop: 1}}>{row.count}</Text> : null}
            </View>
            {row.progress ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
              <Text style={{color: theme.colors.textTertiary, fontSize: 12.5, fontWeight: '700'}}>{row.progress}</Text>
              {row.complete ? <Icon name="i-check" size={18} color={theme.colors.textTertiary}/> : null}
              {!row.complete && row.fraction !== undefined ? <View style={{width: 40, height: 4, borderRadius: 2, backgroundColor: '#edf5fa29'}}><View style={{height: 4, width: `${Math.min(1, row.fraction) * 100}%`, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/></View> : null}
            </View> : null}
          </Focusable>;
        })}
      </ScrollView>
    </View>
  </BottomSheet>;
}
