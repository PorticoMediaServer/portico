import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {WorkspaceSeason} from '../../../../../packages/client-core/src/show-workspace';
import {Button, Focusable, Switch, TextField} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {BottomSheet} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';

export type NativeSeasonsSheetProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  seasons: readonly WorkspaceSeason[];
  total: number;
  selectedId: string | null;
  onSelect: (seasonId: string) => void;
  onMore?: () => void;
  hideWatched: boolean;
  onHideWatched: (value: boolean) => void;
  returnFocusRef?: React.RefObject<View | null>;
}>;

/** phone-detail frame 5: the season sheet. Jump to leads on a long show, then
 * the server's own season counts and the viewer's watched progress. */
export function NativeSeasonsSheet(props: NativeSeasonsSheetProps) {
  const theme = useTheme(), t = defaultI18n.t, [jump, setJump] = useState(''), viewport = useWindowDimensions();
  const scroll = useRef<ScrollView>(null), first = useRef<View>(null);
  const initialSeason = props.seasons.find(season => season.id === props.selectedId)?.id ?? props.seasons[0]?.id;
  useEffect(() => {if (props.open) setJump('');}, [props.open]);
  const canJump = props.total > 30;
  const go = () => {
    const number = Number.parseInt(jump, 10);
    if (!Number.isSafeInteger(number) || number < 1 || number > props.total) return;
    const season = props.seasons.find(value => value.number === number);
    if (season) {setJump(''); props.onSelect(season.id); props.onOpenChange(false);}
  };
  return <BottomSheet testID="watch-seasons" open={props.open} onOpenChange={props.onOpenChange} title={t('title.seasons')} showClose={false}
    initialFocusRef={first} returnFocusRef={props.returnFocusRef}
    headerContent={<View style={{flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 20, paddingBottom: 10}}>
      <Text accessibilityRole="header" style={{color: theme.colors.text, fontSize: 21, fontWeight: '800', letterSpacing: -0.4}}>{t('title.seasons')}</Text>
      <Text style={{color: theme.colors.textTertiary, fontSize: 14, fontWeight: '600'}}>{defaultI18n.number(props.total, {useGrouping: false})}</Text>
      <View style={{flex: 1}}/>
      <Button ref={!initialSeason ? first : undefined} focusId="watch-seasons-done" testID="watch-seasons-done" variant="text" onPress={() => props.onOpenChange(false)}>{t('action.done')}</Button>
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
        {props.seasons.map(season => {
          const selected = season.id === props.selectedId;
          const count = season.episodeCount !== undefined ? t('count.episodes', {count: season.episodeCount}) : undefined;
          const watched = season.watchedCount, total = season.episodeCount;
          const complete = watched !== undefined && total !== undefined && total > 0 && watched >= total;
          const partial = watched !== undefined && total !== undefined && !complete && watched > 0;
          const progress = complete ? t('card.watched') : partial ? t('title.seasonProgress', {watched, total}) : undefined;
          return <Focusable key={season.id} ref={season.id === initialSeason ? first : undefined} focusId={`watch-season-${season.id}`} testID={`watch-season-${season.id}`} focusKind="row" focusRadius={0} accessibilityRole="button"
            accessibilityState={{selected}} onPress={() => {props.onSelect(season.id); props.onOpenChange(false);}}
            style={{position: 'relative', minHeight: 58, flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 20,
              backgroundColor: selected ? '#409fd424' : 'transparent', borderTopWidth: 1, borderTopColor: selected ? 'transparent' : theme.colors.line}}>
            {selected && <View style={{position: 'absolute', left: 0, top: 10, bottom: 10, width: 3, borderTopRightRadius: 3, borderBottomRightRadius: 3, backgroundColor: theme.colors.accent}}/>}
            <View style={{flex: 1, minWidth: 0}}>
              <Text numberOfLines={1} style={{color: selected ? theme.colors.text : theme.colors.text, fontSize: 16, fontWeight: '700'}}>{season.title || t('title.season', {number: season.number})}</Text>
              {count ? <Text style={{color: theme.colors.textTertiary, fontSize: 12.5, fontWeight: '600', marginTop: 1}}>{count}</Text> : null}
            </View>
            {progress ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
              <Text style={{color: theme.colors.textTertiary, fontSize: 12.5, fontWeight: '700'}}>{progress}</Text>
              {complete ? <Icon name="i-check" size={18} color={theme.colors.textTertiary}/> : null}
              {partial && total ? <View style={{width: 40, height: 4, borderRadius: 2, backgroundColor: '#edf5fa29'}}><View style={{height: 4, width: `${Math.min(1, watched! / total) * 100}%`, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/></View> : null}
            </View> : null}
          </Focusable>;
        })}
      </ScrollView>
    </View>
  </BottomSheet>;
}
