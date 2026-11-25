import {defaultI18n} from '@i18n';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {Button} from '../../../kit/controls';
import {router} from '../../../router';
import type {SettingsContext} from '../Settings';
import type {SettingsController} from '../controller';
import {CustomizeHomeRow} from '../account-appearance/CustomizeHome';
import {createPrivacySettingsSurfaces} from '../privacy-controls';
import {DeviceRowControl} from '../device-rows';
import {RemoteNowPlaying} from '../RemoteNowPlaying';
import {deviceRowState, isDeviceSyntheticRow} from '../device-rows-model';
import {AudioSubtitleLanguageList, SubtitlePreview, isSubtitlePreview, preferredLanguageKind} from '../audio-subtitles-rows';
import {subtitlePreviewPaint} from '../audio-subtitles-model';
import {MusicEqualizerRow, musicEqualizerAvailable} from '../account-music/MusicEqualizerRow';
import type {SettingsRouteEnv, SettingsSectionSurface} from './types';

/** Account preference sections: privacy, quality away-limit, audio/subtitles, device, music, appearance. */
export function createAccountPreferencesSection(env: SettingsRouteEnv): SettingsSectionSurface {
  const {api, context, buildContext, role, viewer, viewerId, uiLocale, appLocale, platform, current, reportError, reportActionError, openSettingsRow} = env;
  const privacy = createPrivacySettingsSurfaces({api, serverId: viewer.serverId, viewerId, isCurrent: current, reportError});
  const deviceFacts = {
    get serverName() {return buildContext.facts.identity.serverName;},
    linkTvAvailable: !!api,
    get serverVersion() {return buildContext.facts.identity.serverVersion;},
  };
  // ST-7: the owner's away-from-home server limit, named as the server Streaming row names it.
  const awayLimit = (controller: SettingsController, selected?: SettingsContext): string | undefined => {
    const shown = settingsRowIndex.get('quality.awayLimit')?.row, away = settingsRowIndex.get('streaming.awayQuality')?.row;
    if (!current() || role !== 'owner' || !shown || !away) return undefined;
    const state = controller.rowState(shown, selected), option = away.control.options?.find(item => item.value === state.value);
    return state.present && option ? defaultI18n.t(option.label) : undefined;
  };

  return {
    directEngine: row => privacy.eligible(row),
    hasEngine: row => {
      if (privacy.eligible(row)) return true;
      if (row.id === 'music.eq') return musicEqualizerAvailable(platform);
      return undefined;
    },
    eligible: (row, selected, controller) => {
      if (row.id === 'quality.awayLimitChange') return current() && role === 'owner' && controller.sectionVisible(selected.section);
      if (isSubtitlePreview(row.id)) return current() && controller.sectionVisible(selected.section);
      if (row.id === 'device.remote') return current() && selected.section.scope === 'account' && selected.section.id === 'device';
      if (isDeviceSyntheticRow(row.id)) return current() && controller.sectionVisible(selected.section) && deviceRowState(row, deviceFacts) !== undefined;
      if (row.id === 'music.eq') return current() && controller.sectionVisible(selected.section) && musicEqualizerAvailable(platform);
      if (row.id === 'appearance.customizeHome') return current() && controller.sectionVisible(selected.section);
      return undefined;
    },
    rowState: (row, selected, controller) => {
      if (row.id === 'quality.awayLimit') {
        const limit = awayLimit(controller, selected);
        return limit ? {...controller.rowState(row, selected), displayValue: limit} : {present: false};
      }
      if (row.id === 'quality.awayLimitChange') return {present: !!awayLimit(controller, selected) && controller.sectionVisible(selected.section)};
      const privacyState = privacy.rowState(row);
      if (privacyState !== undefined) return privacyState;
      if (isSubtitlePreview(row.id)) return {present: current() && controller.sectionVisible(selected.section)};
      if (isDeviceSyntheticRow(row.id)) return deviceRowState(row, deviceFacts);
      if (row.id === 'music.eq') return {present: current() && controller.sectionVisible(selected.section) && musicEqualizerAvailable(platform)};
      if (row.id === 'appearance.customizeHome') return {present: current() && controller.sectionVisible(selected.section)};
      return undefined;
    },
    special: (row, state, rowContext, _openPanel, controller) => {
      if (row.id === 'quality.awayLimitChange') {
        return <Button size="small" variant="glass" disabled={state.disabled} onClick={() => {if (current()) openSettingsRow({scope: 'server', section: 'streaming', row: 'streaming.awayQuality'});}}>{defaultI18n.t(row.control.label ?? row.label)}</Button>;
      }
      const privacyControl = privacy.special(row, state);
      if (privacyControl !== undefined) return privacyControl;
      const languageKind = preferredLanguageKind(row.id);
      if (languageKind) {
        return <AudioSubtitleLanguageList row={row} state={state} locale={uiLocale} appLocale={appLocale}
          onChange={value => {if (current()) controller.change(row, value, rowContext);}} onError={reportActionError} />;
      }
      if (isSubtitlePreview(row.id)) {
        const value = (id: string) => controller.rowState(settingsRowIndex.get(id)!.row, rowContext).value;
        return <SubtitlePreview paint={subtitlePreviewPaint({size: value('audioSubtitles.subtitleSize'), color: value('audioSubtitles.subtitleColor'),
          background: value('audioSubtitles.subtitleBackground'), position: value('audioSubtitles.subtitlePosition')})}
          text={defaultI18n.t('next.settingsSpec.account.audio-subtitles.groups.2.rows.0.control.text')} />;
      }
      if (row.id === 'device.remote') return <RemoteNowPlaying api={api} isCurrent={current} onFailure={reportActionError} />;
      if (isDeviceSyntheticRow(row.id)) return <DeviceRowControl key={context.epoch} row={row} api={api} isCurrent={current} onSwitchServer={() => {if (current()) void router.navigate({to: '/servers'});}} />;
      if (row.id === 'music.eq') return <MusicEqualizerRow platform={platform} isCurrent={current} />;
      if (row.id === 'appearance.customizeHome') return <CustomizeHomeRow api={api} home={context.home} serverId={viewer.serverId} viewerId={viewerId} isCurrent={current} />;
      return undefined;
    },
    dispose: () => {privacy.dispose();},
  };
}
