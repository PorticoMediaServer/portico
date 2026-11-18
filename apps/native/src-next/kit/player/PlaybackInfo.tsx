import React, {useEffect, useRef} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {PlaybackComparisonProjection, PlaybackInfoField} from '../../../../../packages/client-core/src/playback-info-projection';
import {Focusable} from '../controls';
import {nativeType, useTheme} from '../theme';

const t = defaultI18n.t;
const fieldLabels: Record<PlaybackInfoField, MessageId> = {
  container: 'next.info.field.container', codec: 'next.info.field.codec', hdr: 'next.info.field.hdr', bitDepth: 'next.info.field.bitDepth',
  resolution: 'next.info.field.resolution', frameRate: 'next.info.field.frameRate', screen: 'next.info.field.screen',
  format: 'next.info.field.format', channels: 'next.info.field.channels', sampleRate: 'next.info.field.sampleRate',
  output: 'next.info.field.output', track: 'next.info.field.track', shown: 'next.info.field.shown',
};
const sectionLabels = {picture: 'next.info.section.picture', sound: 'next.info.section.sound', subtitles: 'next.info.section.subtitles'} as const;
const routeLabels = {original: 'next.info.routes.original', repackaged: 'next.info.routes.repackaged', converted: 'next.info.routes.converted'} as const;
const routes = ['original', 'repackaged', 'converted'] as const;

export type PlaybackInfoComparisonProps = Readonly<{
  comparison: PlaybackComparisonProjection;
  /** TV: the title, over the route. */
  title?: string;
  /** TV: the scroll offset to come back to, applied once its content has laid out while `canRestore` still holds. */
  restoreOffset?: number; canRestore?: () => boolean;
  /** TV: remember where the list is. */
  onScroll?: (offset: number) => void;
}>;

/** Playback info, the drawn part (playback-info 1, 3, 4, 15): the route (original, repackaged, converted) with its
 * reason and the network, then Picture, Sound and Subtitles, each row the file's value beside what plays here, a
 * changed value marked, and what was lost. On TV the route and the column heads stay still and each section is one
 * focus stop; a phone and tablet keep the inline list. `screens/watch/PlaybackInfoPanel` reads it and passes it in. */
export function PlaybackInfoComparison(props: PlaybackInfoComparisonProps) {
  const theme = useTheme(), {comparison} = props;
  if (theme.isTV) return <TvComparison {...props}/>;
  const label = [theme.type.caption, {color: theme.colors.textSecondary}], value = [theme.type.body, {color: theme.colors.text}];
  return <View testID="watch-playback-info-comparison" style={{gap: 18, padding: 16}}>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(routeLabels[comparison.route])}</Text>
    {!!comparison.explanation && <Text style={value}>{comparison.explanation}</Text>}
    {!!comparison.networkAndBitrate && <Text style={label}>{comparison.networkAndBitrate}</Text>}
    {comparison.sections.map(section => <View key={section.kind} style={{gap: 10}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(sectionLabels[section.kind])}</Text>
      <Text style={label}>{t(routeLabels[section.route])}</Text>
      <View style={{flexDirection: 'row', gap: 12}}><View style={{flex: 1}}/><Text style={[label, {flex: 1}]}>{t('next.watch.info.source')}</Text><Text style={[label, {flex: 1}]}>{t('next.watch.info.delivered')}</Text></View>
      {section.rows.map(row => <View key={row.field} style={{flexDirection: 'row', gap: 12}}>
        <Text style={[label, {flex: 1}]}>{t(fieldLabels[row.field])}</Text><Text style={[value, {flex: 1}]}>{row.source}</Text>
        <Text style={[value, {flex: 1, color: row.changed ? theme.colors.accentStrong : theme.colors.text}]}>{row.delivered}</Text>
      </View>)}
      {section.losses.map((loss, index) => <Text key={index} style={label}>{loss}</Text>)}
    </View>)}
  </View>;
}

/** playback-info frames 3 and 4: the TV panel keeps the route and the column heads still, and each section is one focus stop. */
function TvComparison({comparison, title, restoreOffset = 0, canRestore, onScroll}: PlaybackInfoComparisonProps) {
  const theme = useTheme(), first = useRef<View>(null), scroll = useRef<ScrollView>(null), pending = useRef(restoreOffset);
  const label = nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale);
  const valueType = nativeType({size: 22, weight: 500, tracking: 0, lineHeight: 29}, theme.textScale);
  const routeColour = (route: (typeof routes)[number]) => route === 'original' ? theme.colors.healthy : route === 'repackaged' ? theme.colors.accentStrong : '#e8bc6a';
  useEffect(() => {if (theme.isTV) first.current?.requestTVFocus?.();}, [comparison.route, theme.isTV]);
  return <View testID="watch-playback-info-comparison" style={{flex: 1, minHeight: 0}}>
    {!!title && <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 31.9}, theme.textScale), {color: theme.colors.textSecondary, marginTop: 10, marginBottom: 14}]}>
      <Text style={{fontFamily: theme.type.bodyStrong.fontFamily}}>{title}</Text></Text>}
    <View style={{paddingVertical: 20, paddingHorizontal: 24, borderRadius: 18, backgroundColor: 'rgba(237,245,250,.05)', marginBottom: 22}}>
      <View style={{flexDirection: 'row', alignItems: 'center'}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 14}}>
          {routes.map((route, index) => <React.Fragment key={route}>
            {index > 0 && <View style={{width: 60, height: 2, backgroundColor: 'rgba(237,245,250,.16)'}}/>}
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
              <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: comparison.route === route ? routeColour(route) : 'transparent',
                borderWidth: comparison.route === route ? 0 : 1.5, borderColor: 'rgba(237,245,250,.32)'}}/>
              <Text style={[nativeType({size: 22, weight: comparison.route === route ? 800 : 600, tracking: 0, lineHeight: 'normal'}, theme.textScale),
                {color: comparison.route === route ? theme.colors.text : theme.colors.textTertiary}]}>{t(routeLabels[route])}</Text>
            </View>
          </React.Fragment>)}
        </View>
        {!!comparison.networkAndBitrate && <Text style={[label, {marginLeft: 'auto', color: theme.colors.textTertiary}]}>{comparison.networkAndBitrate}</Text>}
      </View>
      {!!comparison.explanation && <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 31.9}, theme.textScale), {color: theme.colors.textSecondary, marginTop: 12}]}>{comparison.explanation}</Text>}
    </View>
    <View style={{flexDirection: 'row', columnGap: 24, paddingHorizontal: 24, paddingBottom: 10, marginBottom: 4, borderBottomWidth: 1.5, borderColor: 'rgba(40,64,79,.9)'}}>
      <View style={{width: 160}}/>
      <Text style={[nativeType({size: 19, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {flex: 1, color: theme.colors.textTertiary}]}>{t('next.info.inFile')}</Text>
      <Text style={[nativeType({size: 19, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {flex: 1, color: theme.colors.textTertiary}]}>{t('next.info.playingHere')}</Text>
    </View>
    <ScrollView ref={scroll} style={{flex: 1, minHeight: 0}} scrollEventThrottle={32} contentContainerStyle={{paddingTop: 10, paddingBottom: 72}}
      onContentSizeChange={() => {
        // The host remembered this panel's offset for the same panel and fence lifetime.
        const offset = pending.current;
        if (!offset) return;
        pending.current = 0;
        if (canRestore?.()) scroll.current?.scrollTo({y: offset, animated: false});
      }}
      onScroll={event => onScroll?.(event.nativeEvent.contentOffset.y)}>
      {comparison.sections.map((section, index) => <Focusable key={section.kind} ref={index === 0 ? first : undefined} focusId={`watch-info-${section.kind}`} testID={`watch-info-${section.kind}`}
        focusKind="row" focusRadius={18} preferredFocus={index === 0} accessibilityLabel={t(sectionLabels[section.kind])}
        style={({focused}) => ({borderRadius: 18, paddingVertical: 16, paddingHorizontal: 24, marginBottom: 10, backgroundColor: focused ? theme.colors.raisedHigh : 'transparent'})}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, marginBottom: 8}}>
          <Text accessibilityRole="header" style={[nativeType({size: 26, weight: 800, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t(sectionLabels[section.kind])}</Text>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 7}}>
            <View style={{width: 7, height: 7, borderRadius: 3.5, backgroundColor: routeColour(section.route)}}/>
            <Text style={[nativeType({size: 20, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t(routeLabels[section.route])}</Text>
          </View>
        </View>
        <View>
          {section.rows.map(row => <View key={row.field} style={{flexDirection: 'row', columnGap: 24, paddingVertical: 2.5}}>
            <Text style={[nativeType({size: 20, weight: 600, tracking: 0, lineHeight: 26}, theme.textScale), {width: 160, paddingTop: 2, color: theme.colors.textTertiary}]}>{t(fieldLabels[row.field])}</Text>
            <Text style={[valueType, {flex: 1, color: theme.colors.textSecondary}]}>{row.source}</Text>
            <View style={{flex: 1, flexDirection: 'row'}}>
              {row.changed && <View style={{width: 8, height: 8, borderRadius: 4, marginTop: 10, marginRight: 10, backgroundColor: theme.colors.accentStrong}}/>}
              <Text style={[valueType, {flex: 1, color: row.changed ? theme.colors.text : theme.colors.textSecondary, fontWeight: row.changed ? '700' : '500'}]}>{row.delivered}</Text>
            </View>
          </View>)}
        </View>
        {section.losses.map((loss, lossIndex) => <Text key={lossIndex} style={[nativeType({size: 20, weight: 500, tracking: 0, lineHeight: 28}, theme.textScale),
          {marginTop: 10, marginLeft: 184, color: theme.colors.textSecondary}]}>{loss}</Text>)}
      </Focusable>)}
    </ScrollView>
  </View>;
}
