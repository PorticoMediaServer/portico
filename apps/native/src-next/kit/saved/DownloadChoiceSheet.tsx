import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {DownloadEpisodes} from '../../../../../packages/client-core/src/download-requests';
import type {useDownloadChoice} from '../../pages/shared/downloads/useDownloadChoice';
import {Button, Focusable} from '../controls';
import {BottomSheet} from '../overlays';
import {RetainedOverlay} from '../overlays/SurfacePaintHost';
import {StateView} from '../states';
import {useTheme} from '../theme';

const t = defaultI18n.t;
const policies = [
  {id: 'all', label: 'downloads.policyAll'}, {id: 'unwatched', label: 'downloads.policyUnwatched'},
  {id: 'next', label: 'downloads.policyNext'},
] as const;
const counts = [1, 3, 5] as const;

export function DownloadChoiceSheet({title, model}: {title: string; model: ReturnType<typeof useDownloadChoice>}) {
  const theme = useTheme();
  const {permit, editable, episodic, policy, keepNext, options, problem, done, acceptedSingle, storage, refused, missingDevice, checking} = model;
  const card = {backgroundColor: theme.colors.raised, borderRadius: 14};
  const horizontal = {flexDirection: 'row' as const, alignItems: 'center' as const, gap: theme.space.sm};
  const choice = (id: DownloadEpisodes | number, label: string, selected: boolean, onPress: () => void, testID?: string) =>
    <Focusable key={id} testID={testID} accessibilityRole="button" accessibilityState={{selected}} disabled={!editable} onPress={onPress}
      focusRadius={12} style={[horizontal, {minHeight: 48, paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs}]}>
      <Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{label}</Text>
      {selected && <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.accentStrong}}/>}
    </Focusable>;
  const bodyStyle = [theme.type.body, {color: theme.colors.textSecondary}];
  const helpStyle = [theme.type.caption, {color: theme.colors.textTertiary}];
  return <RetainedOverlay permit={permit} allowedTypes={[BottomSheet, React.Fragment, View, Text, Button, Focusable, StateView]}>
    <BottomSheet open onOpenChange={open => {if (!open) model.close();}} testID="native-download-sheet"
      title={t(done ? 'action.download' : 'downloads.chooseQuality')} description={title} fitContent
      footer={done ? <View style={horizontal}>
        <Button variant="glass" onPress={model.close}>{t('action.close')}</Button>
        <Button onPress={model.viewDownloads}>{t('downloads.view')}</Button>
      </View> : undefined}>
      <View style={{gap: theme.space.sm}}>
        {problem && <StateView {...problem} tone="error"/>}
        {done ? <>
          {acceptedSingle && <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('downloads.addedMany', {count: 1})}</Text>}
          <Text style={bodyStyle}>{done}</Text>
        </> : <>
          {refused && <Text style={bodyStyle}>{refused}</Text>}
          {missingDevice && <Text style={bodyStyle}>{t('downloads.noDevice')}</Text>}
          {episodic && <View style={card}>{policies.map(item => choice(item.id, t(item.label), item.id === policy,
            () => model.changePolicy(item.id), `download-policy-${item.id}`))}</View>}
          {episodic && policy === 'next' && <View style={card}>{counts.map(count =>
            choice(count, t('downloads.keepNext', {count}), keepNext === count, () => model.changeKeepNext(count)))}</View>}
          {checking && <Text style={helpStyle} accessibilityLiveRegion="polite">{t('downloads.checking')}</Text>}
          {!!options.length && <View style={card}>{options.map(option => <Focusable key={option.quality}
            testID={`download-quality-${option.quality}`} accessibilityRole="button" disabled={!editable || !option.available}
            onPress={() => {void model.choose(option.quality);}} focusRadius={12}
            style={[horizontal, {minHeight: 56, paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs, opacity: option.available ? 1 : .55}]}>
            <View style={{flex: 1, minWidth: 0}}>
              <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{option.label}</Text>
              {(!!option.size || !!option.notes?.length) && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', gap: theme.space.xs}}>
                {option.size && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{option.size}</Text>}
                {option.notes?.map((note, index) => <Text key={index} style={[theme.type.caption, {color: theme.colors.textSecondary, flexShrink: 1}]}>{note}</Text>)}
              </View>}
            </View>
            {(option.meta || option.available) && <Text style={[...helpStyle, {flexShrink: 1}]}>{option.meta ?? t('action.download')}</Text>}
          </Focusable>)}</View>}
          {storage && <Text style={helpStyle}>{storage}</Text>}
        </>}
      </View>
    </BottomSheet>
  </RetainedOverlay>;
}
