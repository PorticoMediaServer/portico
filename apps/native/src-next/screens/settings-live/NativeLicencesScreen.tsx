import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {BackHandler, FlatList, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {licenceCount, licenceDetail, licenceParagraphs, licenceRowDetail, licenceSections, licenceSourceRows, type LicenceData} from '../../../../../packages/client-core/src/presentation/licences';
import {loadLicences, licenceVariant} from '../../platform/licences';
import {Button, Focusable} from '../../kit/controls';
import {StateView} from '../../kit/states';
import {useTheme, nativeType} from '../../kit/theme';

type Row = Readonly<{id: string; title: string; body?: string; componentId?: string; heading?: boolean}>;

/** Generated notices are read after mount; only nearby rows/paragraphs are mounted. */
export function NativeLicencesScreen({onBack, current}: {onBack: () => void; current: () => boolean}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [data, setData] = useState<LicenceData | null>(), [selected, setSelected] = useState<string>();
  const list = useRef<FlatList<Row>>(null);
  useEffect(() => {
    const timer = setTimeout(() => {
      if (!current()) return;
      try {setData(loadLicences() ?? null);} catch {setData(null);}
    }, 0);
    return () => clearTimeout(timer);
  }, [current]);
  const back = useCallback(() => {if (!current()) return; if (selected) setSelected(undefined); else onBack();}, [current, selected, onBack]);
  useEffect(() => {
    const subscription = BackHandler.addEventListener('hardwareBackPress', () => {if (!current()) return false; back(); return true;});
    return () => subscription.remove();
  }, [current, back]);
  const detail = useMemo(() => data && selected ? licenceDetail(data, selected) : undefined, [data, selected]);
  const rows = useMemo((): Row[] => {
    if (!data) return [];
    if (detail) return [
      {id: 'facts', title: detail.component.name, body: licenceRowDetail(detail.component)},
      ...(detail.component.url ? [{id: 'website', title: t('licenses.website'), body: detail.component.url}] : []),
      ...detail.copyright.map((body, index) => ({id: `copyright-${index}`, title: index === 0 ? t('licenses.copyright') : '', body})),
      {id: 'text', title: t('licenses.text'), heading: true},
      ...(detail.text ? licenceParagraphs(detail.text).map((body, index) => ({id: `paragraph-${index}`, title: '', body}))
        : [{id: 'missing', title: '', body: t(detail.component.textAtUrl ? 'licenses.textAtWebsite' : 'licenses.textMissing')}]),
    ];
    const sections = licenceSections(data, licenceVariant()), portico = sections.find(section => section.group === 'portico');
    const componentRows = (section: typeof sections[number]): Row[] => [
      {id: `section-${section.group}`, title: t(section.title), heading: true},
      ...section.components.map(component => ({id: component.id, title: component.name, body: licenceRowDetail(component), componentId: component.id})),
    ];
    return [
      ...(portico ? componentRows(portico) : []),
      {id: 'source-title', title: t('licenses.source.title'), heading: true},
      ...licenceSourceRows(data).map(source => ({id: `source-${source.id}`, title: t(source.label), body: source.url})),
      {id: 'count', title: t('licenses.count', {count: licenceCount(data, licenceVariant())})},
      ...sections.filter(section => section.group !== 'portico').flatMap(componentRows),
    ];
  }, [data, detail, t]);
  useEffect(() => {list.current?.scrollToOffset({offset: 0, animated: false});}, [selected]);
  const first = rows.find(row => !row.heading)?.id;
  return <View testID="native-licenses" style={{flex: 1, paddingHorizontal: theme.isTV ? 48 : 20, paddingTop: theme.isTV ? 48 : 20}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 20, marginBottom: 20}}>
      <Button variant="glass" icon="i-left" onPress={back}>{t('action.back')}</Button>
      <Text accessibilityRole="header" style={[nativeType({size: theme.isTV ? 44 : 30, weight: 800, lineHeight: 'normal', tracking: -.03}, theme.textScale), {color: theme.colors.text, flex: 1}]}>{detail?.component.name ?? t('licenses.title')}</Text>
    </View>
    {data === undefined ? <StateView title={t('licenses.loading')}/> : !data ? <StateView title={t('licenses.unavailable')}/> :
      <FlatList<Row> key={selected ?? 'list'} ref={list} data={rows} keyExtractor={row => row.id} initialNumToRender={10} maxToRenderPerBatch={10} windowSize={5}
        contentContainerStyle={{paddingBottom: 48}} renderItem={({item}) => {
          const content = <>
            {!!item.title && <Text accessibilityRole={item.heading ? 'header' : undefined} style={[nativeType({size: theme.isTV ? item.heading ? 24 : 28 : item.heading ? 16 : 18, weight: item.heading ? 700 : 600, lineHeight: 'normal', tracking: 0}, theme.textScale), {color: item.heading ? theme.colors.textTertiary : theme.colors.text}]}>{item.title}</Text>}
            {!!item.body && <Text style={[nativeType({size: theme.isTV ? 22 : 14, lineHeight: theme.isTV ? 33 : 21, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary, marginTop: 6}]}>{item.body}</Text>}
          </>;
          return item.heading ? <View style={{paddingVertical: 18}}>{content}</View> :
            <Focusable testID={`license-${encodeURIComponent(item.id)}`} accessibilityRole={item.componentId ? 'button' : 'text'} accessibilityLabel={[item.title, item.body].filter(Boolean).join(', ')}
              hasTVPreferredFocus={theme.isTV && item.id === first} onPress={item.componentId ? () => {if (current()) setSelected(item.componentId);} : undefined}
              style={({focused}) => ({paddingHorizontal: 18, paddingVertical: 16, borderRadius: 14, backgroundColor: focused ? theme.colors.raisedHigh : theme.colors.raised, marginBottom: 6})}>{content}</Focusable>;
        }}/>} 
  </View>;
}
