import React from 'react';
import {Platform, ScrollView, StyleSheet, Text, View} from 'react-native';
import type {FactPart, FileInfoModel, ManageAction} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme, type Theme} from '../../kit/theme';
import Svg, {Defs, RadialGradient, Rect, Stop} from 'react-native-svg';
import type {NativeFileInfoDisc, NativeFileInfoLocation} from './file-info';

/** Real entry identity for the drawn context line under the sheet title. Never inferred. */
export type NativeFileInfoIdentity = Readonly<{series?: string; code?: string}>;
export type NativeFileInfoContentProps = Readonly<{
  /** Constructed from the real lazy File info projection, never DetailProjection.files. */
  model: FileInfoModel;
  /** Physical paths retain exact member IDs beneath their authoritative version group. */
  locations?: readonly NativeFileInfoLocation[];
  /** Series name and episode code from the opened entry; absent for a film or a book. */
  identity?: NativeFileInfoIdentity;
  /** Each asset's own probed disc title; absent when the server measured none. */
  disc?: readonly NativeFileInfoDisc[];
  onManage: (action: ManageAction['id'], fileId?: string) => void;
  /** Every version of the title, when they arrive window by window. */
  versionCount?: number;
  /** The next window of versions, of one version's tracks, or of its files. */
  more?: NativeFileInfoMore;
  /** Earlier versions were let go to keep the list bounded; this reads from the first again. */
  earlier?: NativeFileInfoMore;
  trackMore?: (versionId: string) => NativeFileInfoMore | undefined;
  filesMore?: (versionId: string) => NativeFileInfoMore | undefined;
}>;
export type NativeFileInfoMore = Readonly<{busy: boolean; failed: boolean; onPress: () => void}>;
/** The mock-ups' quiet continuation: a small glass "Show more" under its list. */
function More({more, label, focusId}: {more?: NativeFileInfoMore; label: string; focusId: string}) {
  if (!more) return null;
  return <View style={{alignItems: 'flex-start', marginTop: 8}}><Button focusId={focusId} testID={focusId} variant="glass" size="small" disabled={more.busy} onPress={more.onPress}>{more.failed ? defaultI18n.t('next.states.tryAgain') : label}</Button></View>;
}
const words = (parts: readonly FactPart[]) => parts.map(part => part.text).join('');
function Parts({parts, comma = false}: {parts: readonly FactPart[]; comma?: boolean}) {
  const theme = useTheme();
  return <Text style={{color: theme.colors.text, fontFamily: theme.type.body.fontFamily, fontSize: 12.5, lineHeight: 19}}>{comma ? words(parts) : parts.map(part => part.text).join(' ')}</Text>;
}

/** Canonical parts already carry their own separators; adjacent facts take the drawn space. */
const adjacent = (parts: readonly FactPart[]) => parts.reduce((text, fact) => !text ? fact.text : `${text}${fact.id.startsWith('separator') ? '' : ' '}${fact.text}`, '');
/** File info names the track's language, then its facts: "English, DTS-HD MA 5.1". */
const trackText = (parts: readonly FactPart[], comma: string) => !parts.length ? '' : `${parts[0]!.text}${parts.length > 1 ? `${comma}${adjacent(parts.slice(1))}` : ''}`;

/** The sheet's own gradient for the owner's Server treatment (tv-detail.html .fio). */
function OwnerSurface({children}: {children: React.ReactNode}) {
  return <View style={{marginTop: 24, borderRadius: 18, overflow: 'hidden', backgroundColor: '#0b1822', paddingTop: 18, paddingHorizontal: 24, paddingBottom: 8}}>
    <View pointerEvents="none" accessible={false} style={StyleSheet.absoluteFill}><Svg width="100%" height="100%" accessible={false}>
      <Defs><RadialGradient id="watch-file-owner" cx="100%" cy="0%" rx="120%" ry="140%"><Stop offset="0" stopColor="#2f86bb" stopOpacity={0.18}/><Stop offset="0.6" stopColor="#2f86bb" stopOpacity={0}/></RadialGradient></Defs>
      <Rect width="100%" height="100%" fill="url(#watch-file-owner)"/>
    </Svg></View>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: 18, borderWidth: 1.5, borderColor: '#5cb3e229'}]}/>
    {children}
  </View>;
}

const tvType = (theme: Theme, size: number, weight: 400 | 500 | 600 | 700 | 800) => nativeType({size, weight, tracking: 0, lineHeight: 'normal'}, theme.textScale);

/** Informational version cards; no radio, Play button, grants or synthesized file count. */
export function NativeFileInfoContent({model, locations, identity, disc: discFacts, onManage, versionCount, more, earlier, trackMore, filesMore}: NativeFileInfoContentProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const label = {color: theme.colors.textTertiary, fontFamily: theme.type.body.fontFamily, fontSize: 12.5};
  const value = {color: theme.colors.text, fontFamily: theme.type.bodyStrong.fontFamily, fontSize: 12.5};
  const headings = [t('title.version'), t('next.kit.resolution'), t('next.kit.video'), t('next.kit.audio'), t('next.kit.size'), t('next.kit.length')];
  const comma = t('next.kit.fileInfo.comma');
  // A disc image's own probed title and chapter count, per asset. Never inferred.
  const discById = new Map((discFacts ?? []).map(facts => [facts.id, facts]));
  const discTable = model.files.some(file => discById.has(file.id));
  // tv-detail.html frame 8 and playback-info.html frame 19: the drawn column widths, Audio takes the rest.
  const cell = (width: number | undefined, paddingVertical: number) => ({...(width === undefined ? {flex: 1} : {width}), paddingVertical, paddingRight: 12, borderBottomWidth: 1.5, borderColor: theme.colors.line});
  const versions = versionCount ?? model.files.length;
  const rest = [identity?.code, versions > 1 ? t('next.overlays.versionCount', {count: versions}) : undefined].filter((fact): fact is string => !!fact);
  const context = !!identity?.series || !!rest.length;
  return <ScrollView testID="watch-file-info-content" contentContainerStyle={{gap: theme.isTV ? 0 : 10, paddingBottom: 24}}>
    {theme.isTV ? <>
      {context && <View style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>
        {identity?.series ? <Text style={[tvType(theme, 22, 700), {color: theme.colors.text}]}>{identity.series}</Text> : null}
        {rest.map(fact => <Text key={fact} style={[tvType(theme, 22, 400), {color: theme.colors.textTertiary}]}>{fact}</Text>)}
      </View>}
      <View style={{marginTop: context ? 30 : 16, borderTopWidth: 1.5, borderColor: theme.colors.line}}>
        {/* playback-info.html frame 19 draws a disc image's table with Title and Chapters, and no Resolution or Size. */}
        <View style={{flexDirection: 'row'}}>
          <View style={cell(118, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{headings[0]}</Text></View>
          {discTable ? <>
            <View style={cell(116, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{t('next.browse.title')}</Text></View>
            <View style={cell(96, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{t('next.browse.chapters')}</Text></View>
            <View style={cell(150, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{headings[2]}</Text></View>
          </> : <>
            <View style={cell(150, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{headings[1]}</Text></View>
            <View style={cell(124, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{headings[2]}</Text></View>
          </>}
          <View style={cell(undefined, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{headings[3]}</Text></View>
          {discTable ? null : <View style={cell(86, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{headings[4]}</Text></View>}
          <View style={cell(80, 12)}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textTertiary}]}>{headings[5]}</Text></View>
        </View>
        {model.files.map(file => {
          const facts = discById.get(file.id);
          return <View key={file.id} style={{flexDirection: 'row'}}>
            <View style={cell(118, 14)}><Text style={[tvType(theme, 22, 700), {color: theme.colors.text}]}>{file.display.heading.text}</Text></View>
            {discTable ? <>
              <View style={cell(116, 14)}><Text style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{facts ? t('next.overlays.discTitle', {number: facts.titleNumber}) : ''}</Text></View>
              <View style={cell(96, 14)}><Text style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{facts ? String(facts.chapterCount) : ''}</Text></View>
              <View style={cell(150, 14)}><Text style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{adjacent(file.display.video.filter(part => part.id === 'codec' || part.id === 'codec-profile'))}</Text></View>
            </> : <>
              <View style={cell(150, 14)}><Text style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{adjacent(file.rows.find(row => row.id === 'resolution')?.parts ?? [])}</Text></View>
              <View style={cell(124, 14)}><Text style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{adjacent(file.display.video.filter(part => part.id === 'codec' || part.id === 'codec-profile'))}</Text></View>
            </>}
            <View style={cell(undefined, 14)}>{file.display.audio.map(audio => <Text key={audio.id} style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{trackText(audio.parts, comma)}</Text>)}<More more={trackMore?.(file.id)} label={t('next.overlays.fileInfoMoreTracks')} focusId={`watch-file-more-tracks-${file.id}`}/></View>
            {discTable ? null : <View style={cell(86, 14)}><Text style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{adjacent(file.rows.find(row => row.id === 'size')?.parts ?? [])}</Text></View>}
            <View style={cell(80, 14)}><Text style={[tvType(theme, 22, 400), {color: theme.colors.text}]}>{adjacent(file.rows.find(row => row.id === 'length')?.parts ?? [])}</Text></View>
          </View>;
        })}
      </View>
      <More more={earlier} label={t('next.overlays.fileInfoFirstVersion')} focusId="watch-file-first-version"/><More more={more} label={t('next.overlays.fileInfoMoreVersions')} focusId="watch-file-more-versions"/>
    </> : <>{model.files.map(file => <View key={file.id} style={{backgroundColor: '#172e3c', borderRadius: 14, padding: 14}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginBottom: 12}}>
        <View style={{paddingHorizontal: 6, paddingVertical: 2, borderRadius: 4, backgroundColor: '#244555'}}><Text style={[label, {fontSize: 11, color: theme.colors.accentStrong}]}>{file.display.heading.text}</Text></View>
        <Text style={[value, {fontSize: 15, flex: 1}]}>{file.display.summary.map(part => part.text).join(' ')}</Text>
      </View>
      {(['resolution', 'video', 'audio', 'subtitles', 'size', 'length'] as const).map(id => {
        const row = file.rows.find(candidate => candidate.id === id);
        const parts = id === 'video' ? file.display.video : id === 'subtitles' ? file.display.subtitles : row?.parts;
        if (id === 'audio' && !file.display.audio.length || id !== 'audio' && !parts?.length) return null;
        return <View key={id} style={{flexDirection: 'row', gap: 10, marginBottom: 5}}>
          <Text style={[label, {width: 70}]}>{id === 'audio' ? t('next.kit.audio') : id === 'subtitles' ? t('next.kit.subtitles') : row?.label}</Text>
          <View style={{flex: 1}}>{id === 'audio' ? file.display.audio.map(audio => <Parts key={audio.id} parts={audio.parts} comma/>) : <Parts parts={parts ?? []} comma={id === 'video' || id === 'subtitles'}/>}</View>
        </View>;
      })}
      <More more={trackMore?.(file.id)} label={t('next.overlays.fileInfoMoreTracks')} focusId={`watch-file-more-tracks-${file.id}`}/>
    </View>)}<More more={earlier} label={t('next.overlays.fileInfoFirstVersion')} focusId="watch-file-first-version"/><More more={more} label={t('next.overlays.fileInfoMoreVersions')} focusId="watch-file-more-versions"/></>}
    <Text style={[label, {fontSize: theme.isTV ? 20 : 12.5, lineHeight: theme.isTV ? 28 : 19, marginTop: theme.isTV ? 14 : 2}]}>{t('next.overlays.native.fileInfoNote')}</Text>
    {model.owner && theme.isTV ? <OwnerSurface>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, paddingBottom: 14}}>
        <Icon name="i-server" size={26} color={theme.colors.textTertiary}/>
        <Text style={[tvType(theme, 22, 700), {color: theme.colors.text, flex: 1}]}>{model.owner.heading}</Text>
        <View style={{paddingHorizontal: 12, paddingVertical: 4, borderRadius: 8, backgroundColor: '#07101899'}}><Text style={[tvType(theme, 18, 700), {color: theme.colors.textSecondary}]}>{t('next.overlays.native.ownerOnly')}</Text></View>
      </View>
      {(locations ?? model.owner.locations).map(location => <View key={location.id} style={{borderTopWidth: 1.5, borderColor: '#28404fb3', paddingVertical: 12}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 18}}>
          <Text style={[tvType(theme, 20, 600), {color: theme.colors.textTertiary, width: 150}]}>{model.files.find(file => file.id === location.versionId)?.display.heading.text}</Text>
          {!!location.path && <Text selectable style={[tvType(theme, 19, 400), {color: theme.colors.textSecondary, flex: 1, lineHeight: 26, fontFamily: Platform.select({ios: 'Menlo', default: 'monospace'})}]}>{location.path}</Text>}
        </View>
        {!!location.actions.length && <View style={{flexDirection: 'row', gap: 12, marginTop: 14}}>{location.actions.map(action => <Button key={action.id} focusId={`watch-file-${location.id}-${action.id}`} testID={`watch-file-${location.id}-${action.id}`} variant="glass" size="small" icon="i-trash" onPress={() => onManage(action.id, location.id)}>{action.label}</Button>)}</View>}
      </View>)}
      {model.files.map(file => <More key={file.id} more={filesMore?.(file.id)} label={t('next.overlays.fileInfoMoreFiles')} focusId={`watch-file-more-files-${file.id}`}/>)}
      {!!model.owner.actions.filter(action => action.id !== 'merge-title').length && <View style={{flexDirection: 'row', gap: 12, paddingTop: 14, paddingBottom: 8}}>
        {model.owner.actions.filter(action => action.id !== 'merge-title').map(action => <Button key={action.id} focusId={`watch-file-owner-${action.id}`} testID={`watch-file-owner-${action.id}`} variant="glass" size="small" icon={action.id === 'split-versions' ? 'i-split' : 'i-merge'} onPress={() => onManage(action.id)}>{action.label}</Button>)}
      </View>}
      <Text style={[tvType(theme, 18, 400), {color: theme.colors.textTertiary, lineHeight: 26, paddingTop: 4, paddingBottom: 12}]}>{t('next.overlays.deleteVersionNote')}</Text>
    </OwnerSurface> : model.owner && <View style={{marginTop: 16, borderWidth: 1, borderColor: theme.colors.line, borderRadius: 16, overflow: 'hidden'}}>
      <View style={{flexDirection: 'row', alignItems: 'center', padding: 14, gap: 12, backgroundColor: '#153140'}}>
        <Icon name="i-server" size={20} color={theme.colors.textSecondary}/>
        <Text style={[nativeType({size: 16, weight: 800, tracking: -.015, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, flex: 1}]}>{model.owner.heading}</Text>
        <Text style={[label, {fontSize: 11}]}>{t('next.overlays.native.ownerOnly')}</Text>
      </View>
      <View style={{padding: 14, gap: 12}}>
        {(locations ?? model.owner.locations).map(location => <View key={location.id} style={{gap: 8, borderBottomWidth: 1, borderColor: theme.colors.line, paddingBottom: 14}}>
          <Text style={label}>{model.files.find(file => file.id === location.versionId)?.display.heading.text}</Text>
          {location.path && <Text selectable style={[label, {lineHeight: 19}]}>{location.path}</Text>}
          {location.actions.map(action => <Button key={action.id} focusId={`watch-file-${location.id}-${action.id}`} testID={`watch-file-${location.id}-${action.id}`} variant="glass" size="small" icon="i-trash" onPress={() => onManage(action.id, location.id)} textStyle={{color: theme.colors.record}}>{action.label}</Button>)}
        </View>)}
        {model.files.map(file => <More key={file.id} more={filesMore?.(file.id)} label={t('next.overlays.fileInfoMoreFiles')} focusId={`watch-file-more-files-${file.id}`}/>)}
        {model.owner.actions.map(action => <Button key={action.id} focusId={`watch-file-owner-${action.id}`} testID={`watch-file-owner-${action.id}`} variant="glass" size="small" onPress={() => onManage(action.id)}>{action.label}</Button>)}
        <Text style={[label, {lineHeight: 19}]}>{t('next.overlays.deleteVersionNote')}</Text>
      </View>
    </View>}
  </ScrollView>;
}
