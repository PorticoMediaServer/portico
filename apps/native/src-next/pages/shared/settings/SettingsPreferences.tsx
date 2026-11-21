import React from 'react';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {COMMON_LANGUAGES, languageName, MAX_LANGUAGES} from '../../../../../../packages/client-core/src/presentation/language-list';
import {localeChoices} from '../../../../../../packages/client-core/src/presentation/region-choices';
import {readComposite, settingsRowId, settingsScopeLabel, type SettingsRow as Row} from '../../../../../../packages/client-core/src/presentation/settings-structure';
import {Button, Select, Switch} from '../../../kit/controls';
import {ChoiceMatrix, LanguageList, SettingsRow} from '../../../kit/shell';
import {SectionCard} from '../../../kit/frames';
import {SettingsControlRows, SettingsManagementCard} from '../../../kit/settings/SettingsParts';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {StateView} from '../../../kit/states';
import {Dialog, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {nativeSettingsActionTarget} from '../../../screens/settings-live/settings-actions';
import {NativeSafeModeRow} from '../../../screens/settings-live/NativeSafeModeRow';
import {NativeApplePassthroughRow} from '../../../screens/settings-live/NativeApplePassthroughRow';
import {NativeFrameRateRow} from '../../../screens/settings-live/NativeFrameRateRow';
import {NativePassthroughRow} from '../../../screens/settings-live/NativePassthroughRow';
import {NativeDynamicRangeRow} from '../../../screens/settings-live/NativeDynamicRangeRow';
import {NativeLossNoticesRow} from '../../../screens/settings-live/NativeLossNoticesRow';
import {DeviceCapabilityRows} from '../../../kit/settings/DeviceCapabilityRows';
import {NativeServerOfflineNoticeRow} from '../../../screens/settings-live/NativeServerOfflineNoticeRow';
import {passthroughFormats} from '../../../platform/playback/passthrough-formats';
import {playerEngineHints, useSettingsPreferences, type SettingsPreferencesProps} from './useSettingsPreferences';

const SKIP_KEYS: readonly string[] = ['playback.introSkip', 'playback.creditsSkip', 'playback.recapSkip'];

export function SettingsPreferences(props: SettingsPreferencesProps) {
  const {page, ports, controller, runtime, devicePreferences, region, column, reportedOutputs, awayLimitLabel, onStreaming, onQuality, verticalEdges} = props;
  const {t, state, isTV, android, apple, navigation, device, rowIds, hostedRows, sections, hasAdvanced, advanced, setAdvanced,
    visible, enabled, change, showSafeMode, showDynamicRange, skipMatrix, managementURL, actionError, setActionError, actionBusy, confirmFlight,
    shown, openConfirm, closeConfirm, confirm, cancel, engineShown, openEngine, closeEngine, selectEngine,
    choices, setChoices, languages, setLanguages, firstLanguage} = useSettingsPreferences(props);
  return <><SettingsControlRows id={region} entry={hostedRows ? rowIds[0] : undefined}
    // A TV's Switch server page enters through its chooser's region (pages/tv/accounts/ServerChooserPage).
    entryRegion={isTV && page.id === 'servers' ? 'accounts.chooser' : undefined} entryPolicy="entry" order={hostedRows ? rowIds : undefined} orientation="vertical" edges={{left: column, ...verticalEdges}}>
    {actionError !== undefined && <StateView {...runtime.error(actionError, () => setActionError(undefined), 'settings')}/>}
    {sections.map(section => <React.Fragment key={section.id}><SectionCard label={section.title && t(section.title)} help={section.description && t(section.description)} plain={isTV && (section.id === 'note' || page.id === 'servers')}>
      {section.rows.filter(visible).map((row, index) => {
        const id = settingsRowId(row);
        // Frame 12's Apple passthrough is the rows.7 capability line, not the rows.6
        // receiver choice (same visible label, distinct key). The Apple engines have no
        // stored passthrough control, so the choice is suppressed and only the rows.7 row
        // is replaced by the read-only eligibility line.
        if (isTV && apple && page.id === 'device') {
          if (row.label === 'next.settingsSpec.account.device.groups.1.rows.6.label') return null;
          if (row.label === 'next.settingsSpec.account.device.groups.1.rows.7.label') return <NativeApplePassthroughRow key={id} testID={id}/>;
        }
        if (row.kind === 'custom') return <React.Fragment key={id}>{ports.renderSettingsPanel(row.id, runtime)}</React.Fragment>;
        // MOB-023: on tablet touch layouts Intro/Credits/Recap skipping is one choice matrix.
        if (skipMatrix && row.kind === 'preference' && SKIP_KEYS.includes(row.key)) {
          if (row.key !== SKIP_KEYS[0]) return null;
          const skipRows = section.rows.filter(visible).filter((value): value is Extract<Row, {kind: 'preference'}> => value.kind === 'preference' && SKIP_KEYS.includes(value.key) && state.preferences.effective[value.key] !== undefined);
          const choices = row.control.type === 'choice' ? row.control.choices : [];
          if (!skipRows.length || !choices.length) return null;
          return <ChoiceMatrix key={id} testID="settings-skipping" rows={skipRows.map(value => ({id: value.key, label: t(value.label), value: String(state.preferences.effective[value.key])}))}
            options={choices.map(choice => ({value: String(choice.value), label: t(choice.label, choice.labelValues), disabled: state.busy}))}
            onChange={(key, next) => {const target = skipRows.find(value => value.key === key); const choice = choices.find(value => String(value.value) === next); if (target && choice) change(target, choice.value);}}/>;
        }
        if (row.kind === 'server-setting') throw new Error('native_account_page_server_row');
        if (row.kind === 'action') {
          const target = nativeSettingsActionTarget(row.id);
          const act = () => {
            controller.lifetime.assert();
            if (target === 'linkTV') ports.accountActions.linkTV();
            else if (target === 'customizeHome') ports.context.navigation.open({kind: 'home-customize'}, ports.context.authority());
            else if (target === 'quality') onQuality();
            else openConfirm(row.id === 'signOut' ? 'signOut' : 'resetRecommendations');
          };
          // On a touch screen an action that asks to confirm is the row's glass button (settings spec `action`, as the
          // library settings rows draw it); a row that opens a page keeps its chevron.
          const button = !isTV && target === 'confirm' && row.id === 'resetRecommendations';
          return <SettingsRow key={id} testID={button ? undefined : id} destructive={row.destructive} navigates={target !== 'confirm'} label={t(row.label)} description={row.help ? t(row.help) : undefined}
            value={row.value ? t(row.value) : undefined} first={index === 0} onPress={button ? undefined : act}
            control={button ? <Button testID={id} variant="glass" size="small" onPress={act}>{t('settings.resetRecommendations.confirm')}</Button> : undefined}/>;
        }
        let value = row.kind === 'preference' ? state.preferences.effective[row.key] : row.storage === 'registry' ? readComposite(row.id, key => state.preferences.effective[key]) : devicePreferences.read(row.id);
        // No override stored: the drawn TV choice shows what the set reports. Both
        // facts must be known, or the row stays out rather than guessing one.
        if (value === undefined && row.kind === 'composite' && row.id === 'tvSupports' && reportedOutputs)
          value = reportedOutputs.hdr ? reportedOutputs.surround ? 'hdr-surround' : 'hdr' : reportedOutputs.surround ? 'surround' : 'none';
        if (value === undefined) return null;
        const scope = settingsScopeLabel(row.scope, isTV ? 'tv' : 'phone');
        const copy = {label: t(row.label), description: row.help ? t(row.help) : undefined, scope: scope ? t(scope) : undefined, first: index === 0, dependent: !!row.when || !!row.enableWhen};
        const control = row.control;
        if (isTV && control.type !== 'switch') {
          if (control.type === 'languages') {
            const selected = (Array.isArray(value) ? value : []).map(language => ({id: language, label: languageName(language, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}));
            const available = COMMON_LANGUAGES.map(language => ({id: language, label: languageName(language, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}));
            return <SettingsRow key={id} testID={id} {...copy} value={selected.length ? defaultI18n.list(selected.map(language => language.label)) : t('next.shell.native.noLanguages')}
              onPress={() => setLanguages({testID: id, title: copy.label, help: copy.description, value: selected, available, limit: MAX_LANGUAGES, apply: next => change(row, next.map(language => language.id))})}/>;
          }
          const options = control.type === 'choice' ? control.choices.map(choice => {
            const hint = id === 'playerEngine' ? playerEngineHints[String(choice.value)] : undefined;
            return {value: String(choice.value), label: t(choice.label, choice.labelValues), description: hint ? t(hint) : undefined};
          }) : localeChoices(defaultI18n, String(value)).map(choice => ({value: choice.id, label: choice.label}));
          const current = String(value);
          const currentLabel = options.find(option => option.value === current)?.label;
          return <SettingsRow key={id} testID={id} {...copy} value={currentLabel}
            onPress={() => setChoices({title: copy.label, help: copy.description, value: current, options, apply: next => {
              if (control.type === 'choice') {const choice = control.choices.find(choice => String(choice.value) === next); if (choice) change(row, choice.value);}
              else change(row, next);
            }})}/>;
        }
        if (control.type === 'choice' && id === 'playerEngine') {
          const offered = control.choices;
          const options = offered.map(choice => {
            const hint = playerEngineHints[String(choice.value)];
            return {value: String(choice.value), label: t(choice.label, choice.labelValues), description: hint ? t(hint) : undefined};
          });
          return <SettingsRow key={id} testID={id} {...copy} value={options.find(option => option.value === String(value))?.label}
            readable={state.busy} onPress={state.busy ? undefined : () => openEngine(row, value, offered, options)}/>;
        }
        const choiceList = control.type === 'choice' ? control.choices : undefined;
        if (isTV && control.type === 'switch') {
          const on = value === true, blocked = state.busy || !enabled(row);
          // The whole row is the stop and OK flips it; the switch is drawn, never focused.
          return <SettingsRow key={id} testID={id} {...copy} readable={blocked} toggle={{checked: on, visual: <Switch checked={on} focusable={false} accessibilityLabel={t(row.label)} disabled={blocked}/>}}
            onPress={blocked ? undefined : () => change(row, !on)}/>;
        }
        return <SettingsRow key={id} testID={id} {...copy} stack={control.type === 'languages'}
          control={control.type === 'switch' ? <Switch testID={id} checked={value === true} accessibilityLabel={t(row.label)} disabled={state.busy || !enabled(row)} onCheckedChange={next => change(row, next)}/>
            : choiceList ? (() => {const options = choiceList.map(choice => ({value: String(choice.value), label: t(choice.label, choice.labelValues)})); const selected = options.some(option => option.value === String(value)) ? String(value) : undefined; return <Select testID={id} accessibilityLabel={t(row.label)} value={selected} options={options} onValueChange={next => {const choice = choiceList.find(choice => String(choice.value) === next); if (choice) change(row, choice.value);}} disabled={state.busy}/>;})()
              : control.type === 'languages' ? <LanguageList testID={id} label={t(row.label)} value={(Array.isArray(value) ? value : []).map(id => ({id, label: languageName(id, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}))} available={COMMON_LANGUAGES.map(id => ({id, label: languageName(id, key => defaultI18n.has(key) ? t(key as Parameters<typeof t>[0]) : undefined)}))} limit={MAX_LANGUAGES} onChange={next => change(row, next.map(value => value.id))}/>
                : <Select testID={id} accessibilityLabel={t(row.label)} value={String(value)} options={localeChoices(defaultI18n, String(value)).map(choice => ({value: choice.id, label: choice.label}))} onValueChange={next => change(row, next)} disabled={state.busy}/>}/>;
      })}
      {/* PX-05 sits under the Notification dot; the shared structure stays web's (Portico Account only). */}
      {!isTV && page.id === 'profile' && section.id === 'notifications' && <NativeServerOfflineNoticeRow context={ports.context} runtime={runtime} lifetime={controller.lifetime}/>}
      {showSafeMode && section.id === 'tvOutputs' && <NativeSafeModeRow source={devicePreferences} runtime={runtime} current={controller.lifetime.current}/>}
      {section.id === 'limits' && awayLimitLabel ? <>
        <SettingsRow testID="settings-away-limit" label={t('next.settingsSpec.account.quality.groups.0.rows.3.label')} description={t('next.settingsSpec.account.quality.groups.0.rows.3.help')} value={awayLimitLabel}/>
        {!isTV && <SettingsRow navigates testID="settings-away-limit-change" label={t('next.settingsSpec.account.quality.groups.0.rows.3.children.0.label')} onPress={onStreaming}/>}
      </> : null}
    </SectionCard>
    {isTV && page.id === 'device' && section.id === 'device' && (
      <SectionCard label={t('next.settingsSpec.account.device.groups.1.title')}>
        {android && (
          <>
            <NativeFrameRateRow context={ports.context} runtime={runtime}/>
            <NativePassthroughRow context={ports.context} runtime={runtime}/>
          </>
        )}
        {showDynamicRange && <NativeDynamicRangeRow context={ports.context} runtime={runtime}/>}
        <NativeLossNoticesRow context={ports.context} runtime={runtime}/>
      </SectionCard>
    )}
    {isTV && android && page.id === 'device' && advanced && section.id === 'about' && (
      <>
        <DeviceCapabilityRows/>
        <SectionCard label={t('next.settingsSpec.account.device.advanced.groups.1.title')}>
          {passthroughFormats.map(format => (
            <NativePassthroughRow key={format} format={format} context={ports.context} runtime={runtime}/>
          ))}
        </SectionCard>
      </>
    )}
    {managementURL && section.id === 'who' && <SettingsManagementCard url={managementURL} text={runtime.text}/>}</React.Fragment>)}
    {hasAdvanced && <NativeAdvancedSection testID="settings-advanced" summary="" open={advanced} onOpenChange={setAdvanced}>{null}</NativeAdvancedSection>}
  </SettingsControlRows>
    <Dialog testID="settings-action-confirm" open={shown !== undefined} onOpenChange={open => {if (!open && !confirmFlight.current && shown) closeConfirm(shown);}} initialFocusRef={cancel}
      title={shown?.kind === 'signOut' ? t('settings.signOutConfirm.title', {server: ports.serverName(), device: t(device)}) : t('settings.resetRecommendations.confirmTitle')}
      description={t(shown?.kind === 'signOut' ? isTV ? 'settings.signOutConfirm.bodyTV' : 'settings.signOutConfirm.bodyDownloads' : 'settings.resetRecommendations.confirmBody')}
      children={null} footer={<OverlayFootRow layout="end">
        <Button focusId="cancel" ref={cancel} variant="glass" disabled={actionBusy}
          onPress={() => {if (shown) closeConfirm(shown);}}>{t('action.cancel')}</Button>
        <Button focusId="confirm" variant="danger" loading={actionBusy} onPress={confirm}>
          {t(shown?.kind === 'signOut' ? 'action.signOut' : 'settings.resetRecommendations.confirm')}
        </Button>
      </OverlayFootRow>}/>
    {isTV && <Menu testID="settings-value-sheet" open={choices !== undefined} onOpenChange={open => {if (!open) setChoices(undefined);}} presentation="sideSheet"
      title={choices?.title} description={choices?.help} initialFocusId={choices?.value}
      items={choices ? choices.options.map(option => ({id: option.value, label: option.label, description: option.description, selected: option.value === choices.value, onSelect: () => {const apply = choices.apply; setChoices(undefined); apply(option.value);}})) : []}/>}
    {engineShown && <Menu testID="settings-player-engine-sheet" open onOpenChange={open => {if (!open && engineShown) closeEngine(engineShown);}} presentation="sheet"
      title={t('next.settingsSpec.account.device.advanced.groups.3.rows.0.label')} initialFocusId={engineShown.value}
      items={engineShown.options.map(option => ({id: option.value, label: option.label, description: option.description, selected: option.value === engineShown.value,
        onSelect: () => selectEngine(option.value)}))}/>}
    {isTV && <Dialog testID="settings-language-sheet" open={languages !== undefined} onOpenChange={open => {if (!open) setLanguages(undefined);}} title={languages?.title} description={languages?.help} placement="side" width={frameSpec.settings.languageWidth} initialFocusRef={firstLanguage}>
      {languages && <LanguageList initialFocusRef={firstLanguage} testID={languages.testID} label={languages.title} value={languages.value} available={languages.available} limit={languages.limit} onChange={languages.apply}/>}
    </Dialog>}
  </>;
}
