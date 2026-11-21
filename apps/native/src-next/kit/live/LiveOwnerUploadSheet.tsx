import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Button, TextField} from '../controls';
import {Dialog} from '../overlays';
import {StateView} from '../states';
import {useTheme} from '../theme';
import type {NativeLiveOwnerController} from '../../screens/settings-live/live-owner-controller';
import type {UploadPreview} from '../../screens/settings-live/live-owner-model';
import {liveDocumentPicker} from '../../screens/settings-live/live-document-picker';
import {useLiveSourceUpload} from '../../pages/shared/settings/useLiveSourceUpload';

const sizeLabel = (bytes: number) => defaultI18n.bytes(bytes);

export function UploadSourceSheet({controller, runtime, open, onOpenChange, onSaved}: {
  controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void;
}) {
  const theme = useTheme();
  const {t, current, busy, input, reading, files, draft, revision, preview, close, choose, edit, check, save, clearError} = useLiveSourceUpload({controller, runtime, open, onOpenChange, onSaved});
  const fileRow = (field: 'playlist' | 'guide', label: string) => <View style={{gap: 6}}>
    <Text style={[theme.type.rowLabel, {color: theme.colors.textSecondary}]}>{t(label as Parameters<typeof t>[0])}</Text>
    <Button testID={`live-owner-upload-${field}`} variant="glass" icon="i-upload" loading={reading === field} disabled={busy || !!reading}
      onPress={() => {if (current) void choose(current, field);}}>{files[field]?.title ?? t('web.metadata.chooseImageFile')}</Button>
    {files[field] && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{sizeLabel(files[field]!.bytes)}</Text>}
  </View>;
  return <Dialog open={open && !!current} onOpenChange={value => {if (!busy && !value && current) close(current);}} title={t('web.liveTv.upload')} description={t('web.liveTv.uploadLede')} dismissOnBack={!busy} dismissOnOutsidePress={!busy} showClose={!busy}
    footer={<View style={{flexDirection: 'row', gap: 10}}>
      <Button variant="glass" disabled={busy} onPress={() => {if (current) close(current);}}>{t('action.cancel')}</Button>
      <Button variant="primary" loading={busy} disabled={busy || !!reading || !input.name.trim() || !input.playlist}
        onPress={() => {if (current && draft && revision !== undefined) {if (preview) void save(current, revision, draft, preview);else void check(current, revision, draft);}}}>{t(preview ? 'web.liveTv.addSource' : 'web.liveTv.checkFiles')}</Button>
    </View>} testID="live-owner-upload">
    <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{gap: 16}}>
      <TextField testID="live-owner-upload-name" label={t('web.liveTv.sourceName')} value={input.name} maxLength={100} editable={!(busy || !!reading)} onChangeText={name => {if (current) edit(current, {name});}}/>
      {fileRow('playlist', 'web.liveTv.playlistFile')}
      {fileRow('guide', 'web.liveTv.guideFile')}
      <TextField testID="live-owner-upload-tuners" label={t('web.dvrSettings.tuners')} description={t('web.liveTv.tunersHelp')} keyboardType="number-pad"
        value={String(input.tunerCount)} editable={!(busy || !!reading)}
        onChangeText={value => {const count = Number(value);if (current && Number.isSafeInteger(count) && count >= 0 && count <= 64) edit(current, {tunerCount: count});}}/>
      {current && current.error !== undefined && <StateView testID="live-owner-upload-error" {...runtime.error(current.error, clearError)}/>}
      {preview && <View testID="live-owner-upload-preview" style={{backgroundColor: theme.colors.raisedHigh, borderRadius: 12, padding: 14, gap: 6}}>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[t('server.live.channelCount', {count: preview.value.channels}), t('server.live.programCount', {count: preview.value.programmes})].filter(Boolean).map((part, index) => <Text key={index} style={[theme.type.rowLabel, {color: theme.colors.text}]}>{part}</Text>)}</View>
        {!!preview.value.names.length && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{preview.value.names.slice(0, 6).join(', ')}</Text>}
      </View>}
    </ScrollView>
  </Dialog>;
}
