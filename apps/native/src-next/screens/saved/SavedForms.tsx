import React, {useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable, Switch, TextField} from '../../kit/controls';
import {FocusRegion, useFocusRegistry} from '../../kit/focus';
import {browseType} from '../../kit/browse/presentation';
import {NativeTVKeyboard} from '../search/TVKeyboard';
import {Banner, BottomSheet, Dialog, SidePanel, type FocusRef} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';
import type {PersonalVisibility} from '../../../../../packages/client-core/src/personal-saved';

const t = defaultI18n.t;

export type SavedNameSheetProps = Readonly<{
  open: boolean;
  title: string;
  action: string;
  initialName?: string;
  initialSummary?: string;
  withSummary: boolean;
  withVisibility?: boolean;
  initialVisibility?: PersonalVisibility;
  busy: boolean;
  /** An uncertain create retries the original values, without minting a new command. */
  locked?: boolean;
  error?: string;
  testID: string;
  returnFocusRef?: FocusRef;
  onClose: () => void;
  onSave: (input: Readonly<{name: string; summary: string; visibility: PersonalVisibility}>) => void;
}>;

/** The drawn name dialog as a sheet: Name, optional Description and the
 * published visibility switch. Every write is the caller's real command. */
export function SavedNameSheet(props: SavedNameSheetProps) {
  const theme = useTheme();
  return theme.isTV ? <SavedTVNameSheet {...props}/> : <SavedPhoneNameSheet {...props}/>;
}

function SavedPhoneNameSheet(props: SavedNameSheetProps) {
  const theme = useTheme(), input = useRef<React.ComponentRef<typeof TextField>>(null);
  const [name, setName] = useState(props.initialName ?? ''), [summary, setSummary] = useState(props.initialSummary ?? '');
  const [visibility, setVisibility] = useState<PersonalVisibility>(props.initialVisibility ?? 'private');
  useEffect(() => {
    if (!props.open) return;
    setName(props.initialName ?? ''); setSummary(props.initialSummary ?? ''); setVisibility(props.initialVisibility ?? 'private');
  }, [props.open, props.initialName, props.initialSummary, props.initialVisibility]);
  const save = () => {if (!props.busy && name.trim()) props.onSave({name: name.trim(), summary: summary.trim(), visibility});};
  return <BottomSheet open={props.open} onOpenChange={open => {if (!open && !props.busy) props.onClose();}} testID={props.testID}
    title={props.title} initialFocusRef={input} returnFocusRef={props.returnFocusRef} showClose={false} fitContent keyboardAvoiding
    dismissOnBack={!props.busy} dismissOnOutsidePress={!props.busy} dismissOnGesture={!props.busy}
    footer={<View style={{flexDirection: 'row', gap: 12}}>
      <Button testID={`${props.testID}-cancel`} focusId={`${props.testID}-cancel`} variant="glass" disabled={props.busy} onPress={props.onClose} style={{flex: 1}}>{t('action.cancel')}</Button>
      <Button testID={`${props.testID}-save`} focusId={`${props.testID}-save`} variant="primary" loading={props.busy} disabled={props.busy || !name.trim()}
        onPress={save} style={{flex: 1}}>{props.action}</Button>
    </View>}>
    <View style={{gap: 12}}>
      {props.error !== undefined && <Banner testID={`${props.testID}-error`} variant="notice" tone="error" placement="inline" message={props.error}/>}
      <TextField ref={input} testID={`${props.testID}-name`} focusId={`${props.testID}-name`} label={t('profile.name')} value={name} onChangeText={setName}
        editable={!props.busy && !props.locked} maxLength={200} returnKeyType="done" onSubmitEditing={save}/>
      {props.withSummary && <TextField testID={`${props.testID}-summary`} focusId={`${props.testID}-summary`} label={t('web.saved.summary')} value={summary}
        onChangeText={setSummary} editable={!props.busy && !props.locked} maxLength={4000} multiline/>}
      {props.withVisibility && <View style={{gap: 6}}>
        <Switch checked={visibility === 'server'} onCheckedChange={next => setVisibility(next ? 'server' : 'private')} disabled={props.busy || props.locked}>{t('web.saved.visibleToAll')}</Switch>
        <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t(visibility === 'server' ? 'web.saved.sharedHelp' : 'web.saved.privateHelp')}</Text>
      </View>}
    </View>
  </BottomSheet>;
}

const nameKeys = ['abcdefghi', 'jklmnopqr', 'stuvwxyz', '123456789'].map(row => [...row]);

/** TV creation and rename use the shared remote keyboard inside one held side panel. */
function SavedTVNameSheet(props: SavedNameSheetProps) {
  const theme = useTheme(), registry = useFocusRegistry(), instance = React.useId(), id = `${props.testID}.${instance}`;
  const [name, setName] = useState(props.initialName ?? ''), [summary, setSummary] = useState(props.initialSummary ?? '');
  const [visibility, setVisibility] = useState<PersonalVisibility>(props.initialVisibility ?? 'private'), [field, setField] = useState<'name' | 'summary'>('name');
  useEffect(() => {
    if (!props.open) return;
    setName(props.initialName ?? ''); setSummary(props.initialSummary ?? ''); setVisibility(props.initialVisibility ?? 'private'); setField('name');
  }, [props.open, props.initialName, props.initialSummary, props.initialVisibility]);
  const keyboard = `${id}.keyboard`, fields = `${id}.fields`, footer = `${id}.footer`;
  const initial = React.useMemo(() => ({current: {requestTVFocus: () => {registry.focus({regionId: props.locked ? footer : `${keyboard}.row.0`, childId: props.locked ? 'save' : 'a'});}}}), [footer, keyboard, props.locked, registry]);
  const save = () => {if (props.open && !props.busy && name.trim()) props.onSave({name: name.trim(), summary: summary.trim(), visibility});};
  const edit = (next: 'name' | 'summary') => {if (!props.busy && !props.locked) setField(next);};
  return <SidePanel open={props.open} testID={id} width={680} topInset={0} showClose={false} title={props.title}
    initialFocusRef={initial} returnFocusRef={props.returnFocusRef} dismissOnBack={!props.busy} closeOnOutsidePress={!props.busy}
    onOpenChange={open => {if (!open && !props.busy) props.onClose();}}
    titleTextStyle={browseType(theme, 44, 800, -.025)} style={{paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56}}
    footer={<FocusRegion id={footer} orientation="horizontal" entry={name.trim() ? 'save' : 'cancel'} edges={{up: props.withVisibility ? fields : keyboard}} style={{flexDirection: 'row', gap: 16}}>
      <Button focusId="cancel" testID={`${props.testID}-cancel`} variant="glass" disabled={props.busy} onPress={props.onClose} style={{flex: 1}}>{t('action.cancel')}</Button>
      <Button focusId="save" testID={`${props.testID}-save`} variant="primary" loading={props.busy} disabled={props.busy || !name.trim()} onPress={save} style={{flex: 1}}>{props.action}</Button>
    </FocusRegion>}>
    <View style={{gap: 16}}>
      {props.error !== undefined && <Banner testID={`${props.testID}-error`} variant="notice" tone="error" placement="inline" message={props.error}/>}
      <FocusRegion id={fields} entry="name" edges={{down: keyboard, right: keyboard}} style={{gap: 12}}>
        {(['name', ...(props.withSummary ? ['summary'] as const : [])] as const).map(value => <Focusable key={value} focusId={value}
          testID={`${props.testID}-${value}`} disabled={props.busy || props.locked} accessibilityLabel={`${t(value === 'name' ? 'profile.name' : 'web.saved.summary')}: ${value === 'name' ? name : summary}`}
          accessibilityRole="button" onPress={() => {edit(value); registry.focus({regionId: `${keyboard}.row.0`, childId: 'a'});}} onFocus={() => edit(value)}
          style={{width: 500, minHeight: 80, borderRadius: 16, paddingHorizontal: 18, paddingVertical: 12, backgroundColor: 'rgba(237,245,250,.08)'}}>
          <Text style={[browseType(theme, 18, 600), {color: theme.colors.textTertiary}]}>{t(value === 'name' ? 'profile.name' : 'web.saved.summary')}</Text>
          <Text numberOfLines={1} style={[browseType(theme, value === 'name' ? 34 : 24, value === 'name' ? 700 : 400), {color: theme.colors.text}]}>{(value === 'name' ? name : summary) || ' '}</Text>
        </Focusable>)}
        {props.withVisibility && <View style={{gap: 6}}>
          <Switch focusId="visibility" checked={visibility === 'server'} disabled={props.busy || props.locked} onCheckedChange={next => setVisibility(next ? 'server' : 'private')}>{t('web.saved.visibleToAll')}</Switch>
          <Text style={[browseType(theme, 20), {color: theme.colors.textTertiary}]}>{t(visibility === 'server' ? 'web.saved.sharedHelp' : 'web.saved.privateHelp')}</Text>
        </View>}
      </FocusRegion>
      <NativeTVKeyboard id={keyboard} testID={`${props.testID}-keyboard`} width={500} rows={nameKeys} columns={9} lead="0"
        value={field === 'name' ? name : summary} activation={{visible: props.open && !props.busy && !props.locked, intent: 'enter', revision: 0}}
        leftRegionId={fields} rightRegionId={footer} onChangeText={value => {if (!props.open || props.busy || props.locked) return;
          if (field === 'name') setName(value.slice(0, 200)); else setSummary(value.slice(0, 4000));}}/>
    </View>
  </SidePanel>;
}

export type SavedConfirmSheetProps = Readonly<{
  open: boolean;
  title: string;
  body: string;
  action: string;
  busy: boolean;
  error?: string;
  /** Draw the confirm as the destructive red-tint action (collections 13c). */
  destructive?: boolean;
  testID: string;
  onClose: () => void;
  onConfirm: () => void;
}>;

/** A destructive confirm: the phone sheet and the TV dialog share one command. */
export function SavedConfirmSheet(props: SavedConfirmSheetProps) {
  const theme = useTheme();
  const footer = <View style={{flexDirection: 'row', gap: 12}}>
    <Button testID={`${props.testID}-cancel`} focusId={`${props.testID}-cancel`} variant="glass" disabled={props.busy} onPress={props.onClose} style={{flex: 1}}>{t('action.cancel')}</Button>
    <Button testID={`${props.testID}-confirm`} focusId={`${props.testID}-confirm`} variant={props.destructive ? 'glass' : 'primary'} loading={props.busy} disabled={props.busy}
      onPress={props.onConfirm} style={props.destructive ? {flex: 1, backgroundColor: `${theme.colors.record}24`} : {flex: 1}}
      textStyle={props.destructive ? {color: theme.colors.record} : undefined}>{props.action}</Button>
  </View>;
  const body = <View style={{gap: 12}}>
    {props.error !== undefined && <Banner testID={`${props.testID}-error`} variant="notice" tone="error" placement="inline" message={props.error}/>}
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{props.body}</Text>
  </View>;
  if (theme.isTV) return <Dialog open={props.open} onOpenChange={open => {if (!open && !props.busy) props.onClose();}} title={props.title}
    testID={props.testID} width={620} dismissOnBack={!props.busy} dismissOnOutsidePress={!props.busy} footer={footer}>{body}</Dialog>;
  return <BottomSheet open={props.open} onOpenChange={open => {if (!open && !props.busy) props.onClose();}} title={props.title} testID={props.testID}
    fitContent dismissOnBack={!props.busy} dismissOnOutsidePress={!props.busy} dismissOnGesture={!props.busy} footer={footer}>{body}</BottomSheet>;
}
