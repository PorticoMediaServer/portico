import React, {useEffect, useState} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {ShowSettings} from '../../../../../packages/client-core/src/show-workspace';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Switch} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {BottomSheet} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';

export type NativeShowSettingsSheetProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  showTitle: string;
  /** A server-provided episode used only to show what the new label will read. */
  episode?: ContentEntry;
  settings: ShowSettings;
  busy: boolean;
  onSave: (settings: ShowSettings) => void;
}>;

/** phone-detail frame 6: the owner's show settings sheet. Four drawn switches,
 * their verbatim help, and a preview of the resulting episode label. */
export function NativeShowSettingsSheet(props: NativeShowSettingsSheetProps) {
  const theme = useTheme(), t = defaultI18n.t, [value, setValue] = useState(props.settings), viewport = useWindowDimensions();
  useEffect(() => {if (props.open) setValue(props.settings);}, [props.open, props.settings]);
  const change = (key: keyof ShowSettings, next: boolean) => setValue(current => ({...current, [key]: next}));
  const episode = props.episode;
  const absolute = episode?.absoluteNumber ?? episode?.episodeNumber;
  const seasonal = episode?.seasonNumber !== undefined && episode.episodeNumber !== undefined ? {season: episode.seasonNumber, episode: episode.episodeNumber} : undefined;
  const preview = absolute === undefined ? undefined : value.absoluteNumbering ? t('next.kit.episode', {number: absolute}) : t('next.kit.episode', {number: episode!.episodeNumber ?? absolute});
  return <BottomSheet open={props.open} onOpenChange={props.onOpenChange} title={t('title.showSettings')} showClose={false}
    footer={<View style={{flexDirection: 'row', gap: 10}}>
      <Button variant="glass" style={{flex: 1}} disabled={props.busy} onPress={() => props.onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button style={{flex: 1}} disabled={props.busy} onPress={() => props.onSave(value)}>{t('action.save')}</Button>
    </View>}
    detents={[Math.round(viewport.height * .78)]}>
    <ScrollView contentContainerStyle={{paddingBottom: 16}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginHorizontal: 16, padding: 14, borderRadius: 14,
        backgroundColor: '#409fd44d', borderWidth: 1, borderColor: '#5cb3e24a'}}>
        <View style={{width: 36, height: 36, borderRadius: 10, alignItems: 'center', justifyContent: 'center', backgroundColor: '#07101880'}}><Icon name="i-filter" size={20} color={theme.colors.accentStrong}/></View>
        <View style={{flex: 1, minWidth: 0}}>
          <Text numberOfLines={1} style={{color: theme.colors.accentStrong, fontSize: 12, fontWeight: '700'}}>{props.showTitle}</Text>
          <Text style={{color: theme.colors.text, fontSize: 17, fontWeight: '800', letterSpacing: -0.25}}>{t('title.showSettings')}</Text>
        </View>
        <Text style={{color: theme.colors.textSecondary, fontSize: 11.5, fontWeight: '700', paddingHorizontal: 8, paddingVertical: 4, borderRadius: 6, backgroundColor: '#07101880'}}>{t('next.overlays.native.ownerOnly')}</Text>
      </View>
      <Text style={{color: theme.colors.textSecondary, fontSize: 13.5, lineHeight: 20, marginHorizontal: 16, marginTop: 12}}>{t('title.showSettings.lede')}</Text>
      <View style={{marginHorizontal: 16, marginTop: 12}}>
        {([
          ['hideSeasons', 'title.showSettings.hideSeasons', 'title.showSettings.hideSeasonsHelp'],
          ['absoluteNumbering', 'title.showSettings.absolute', 'title.showSettings.absoluteHelp'],
          ['ranges', 'title.showSettings.ranges', 'title.showSettings.rangesHelp'],
          ['newestFirst', 'title.showSettings.newestFirst', 'title.showSettings.newestFirstHelp'],
        ] as const).map(([key, label, help]) => <View key={key} style={{borderTopWidth: 1, borderTopColor: theme.colors.line}}>
          <Switch checked={value[key]} onCheckedChange={next => change(key, next)} description={t(help)} testID={`watch-show-setting-${key}`}>{t(label)}</Switch>
        </View>)}
      </View>
      {episode && preview ? <View style={{marginHorizontal: 16, marginTop: 14, padding: 12, borderRadius: 14, backgroundColor: theme.colors.raisedHigh}}>
        <Text style={{color: theme.colors.textTertiary, fontSize: 12.5, fontWeight: '700', marginBottom: 10}}>{t('next.watch.episodesWillRead')}</Text>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
          <View style={{width: 104, aspectRatio: 16 / 9, borderRadius: 7, backgroundColor: theme.colors.raised}}/>
          <View style={{flex: 1, minWidth: 0}}>
            <Text numberOfLines={1} style={{color: theme.colors.text, fontSize: 14, fontWeight: '700'}}>{episode.displayTitle ?? episode.title}</Text>
            <Text style={{color: theme.colors.textSecondary, fontSize: 12.5, fontWeight: '600', marginTop: 2}}>{preview}
              {value.absoluteNumbering && seasonal ? <Text style={{color: theme.colors.textTertiary}}>{'  '}{t('next.watch.wasSeason', seasonal)}</Text> : null}
            </Text>
          </View>
        </View>
      </View> : null}
    </ScrollView>
  </BottomSheet>;
}
