import type {IconId} from '../../../design/src/icons.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import type {PreferencePatch, PreferenceValue} from '../preferences.ts';
import type {ServerFormId} from '../server-admin/server-forms.ts';

/**
 * The one Settings screen, authored (Justin, 2 Oct 2026). Personal and server settings are one
 * destination: pages listed under two headings, **Account** (personal) and **Server** (owners).
 *
 * The preference registry and the server's capabilities are vocabulary, not layout. This file is
 * the layout: which pages exist, in what order, which rows each holds, what each row says and
 * which control it uses. A registry key no row references does not appear. Every client draws
 * this structure with its own controls — a two-pane screen on the web and iPad, a pushed list on
 * iPhone, a column of pages beside the rows on television — and none of them invents a row.
 *
 * Rows:
 * - `preference`: one registry key (`operations/preferences.go` stays the storage and validation
 *   contract).
 * - `composite`: one decision a person makes that reads and writes several keys ("Playback
 *   method"), or a value this device keeps for itself ("Default library view").
 * - `action`: something done, not set (Sign out, Reset recommendations, Link a TV).
 * - `custom`: a panel the client draws natively (profiles, devices, the subtitle preview).
 * - `server-setting`: one value of a server settings document (`server-admin/server-forms.ts`),
 *   edited as a draft and saved with the page's one Save.
 */

export type SettingsPlatform = 'web' | 'phone' | 'tv';
export type SettingsHeadingId = 'account' | 'server';
/**
 * Whose the value is, said on the row. `profile`: the profile's on this server (it follows them
 * everywhere; the rule, so the row says nothing). `deviceClass`: the profile's for every device of
 * this kind — the server keeps one value for all web browsers, one for all phones and tablets and
 * one for all TVs. `device`: kept by this one device and nowhere else.
 */
export type SettingsScope = 'profile' | 'deviceClass' | 'device';

export type SettingsCapabilities = Readonly<{
  /** Registry keys this server publishes; a `preference` row whose key is missing is left out. Absent: all are assumed. */
  preferenceKeys?: ReadonlySet<string>;
  /** Signed in with an account that lives on this server: profiles, password, two-step and devices are managed here. */
  localAccount?: boolean;
  /** A Portico Account member of this server: password and two-step verification are the account's. */
  porticoMember?: boolean;
  /** A Portico Account sign-in through Portico's servers. */
  hostedAccount?: boolean;
  /** This device can keep downloads. */
  downloads?: boolean;
  /** The client can say what this device is called in device lists. */
  deviceName?: boolean;
  /** Password and two-step verification can be changed here. Absent: a server account that is not a Portico Account member. */
  manageSignIn?: boolean;
  /** This viewer may manage the account's devices. Absent: a local account. */
  manageDevices?: boolean;
  /** The server offers Live TV. */
  liveTV?: boolean;
  /** This device's player can change playback speed. Absent: it can (a Roku's player cannot). */
  playbackSpeed?: boolean;
  /** This device renders audio itself: gapless, crossfade and volume leveling. Absent: it does (a Roku's player cannot). */
  audioEffects?: boolean;
  /** This device's audio engine can match the output rate to the file (Smart/Strict/Off). Absent: it cannot. */
  sampleRateMatching?: boolean;
  /** This device has its own equalizer screen and device-local store. Absent: it has none. */
  equalizer?: boolean;
  /** This build has mpv as a second player beside the device's own, so there is a player to choose (the capability document then has an `mpv` engine). Absent or false: there is not (the web never has). */
  playerEngines?: boolean;
  /** This device can hand sound to a receiver undecoded (the capability document lists `audioPassthrough`). */
  audioPassthrough?: boolean;
  /** This device or its display can show HDR. Absent: it can. */
  hdr?: boolean;
  /** This device cannot tell what its TV and sound system take, so the viewer says whether they show HDR and play surround (Vega). Absent or false: it detects them. */
  declaredOutputs?: boolean;
  /** This device can be laid out for a television or for touch, whatever it reports itself as (an Android device). Absent or false: its layout is fixed. */
  layoutChoice?: boolean;
  /** This member makes personal access tokens for their own scripts. Absent or false: not offered (the owner makes API keys on the Server pages instead). */
  accessTokens?: boolean;
}>;

/**
 * `owner`: the server's owner. `administrator`: an account of the administrator tier, which sees
 * only the Server sections marked for administrators (what the server lets that tier do).
 */
export type SettingsContext = Readonly<{owner: boolean; administrator?: boolean; platform: SettingsPlatform; capabilities: SettingsCapabilities}>;

export type SettingsChoice = Readonly<{value: string | number | boolean; label: MessageId; labelValues?: MessageValues}>;

export type SettingsControl =
  | Readonly<{type: 'switch'}>
  | Readonly<{type: 'choice'; choices: readonly SettingsChoice[]}>
  /** An ordered list of languages, picked by name. Never comma-separated text. */
  | Readonly<{type: 'languages'}>
  | Readonly<{type: 'locale'}>;

export type CompositeId =
  | 'playbackMethod' | 'cardSize' | 'playback.stillWatching'
  // Kept by the device and published in its capability document (`withViewerSettings`).
  | 'playerEngine' | 'forceStereo' | 'disablePassthrough' | 'disableHdr' | 'tvHdr' | 'tvSurround' | 'tvSupports' | 'music.sampleRateMatching'
  | 'libraryView' | 'reduceMotion' | 'textSize' | 'receiveFromDevices'
  // Kept by the device and read before the app draws anything (B-19.10).
  | 'layout';
export type SettingsActionId = 'signOut' | 'resetRecommendations' | 'linkTV' | 'customizeHome' | 'devicePlayerFixes';
export type SettingsCustomId =
  | 'identity' | 'profiles' | 'password' | 'twoStep' | 'devices' | 'porticoAccount' | 'servers'
  | 'subtitlePreview' | 'deviceName' | 'deviceConnectionStatus' | 'deviceServerAddress' | 'deviceTestConnection' | 'deviceInsecureConnections' | 'downloadsStorage' | 'equalizer' | 'about' | 'accessTokens' | 'privacyNote' | 'remoteControls'
  // The Server heading's panels: each is drawn natively by the client.
  | 'server.nowPlaying' | 'server.alerts' | 'server.health' | 'server.activity' | 'server.statistics'
  | 'server.playHistory'
  | 'server.libraries'
  | 'server.liveSources' | 'server.tuners' | 'server.recordingRules' | 'server.recordingStorage' | 'server.channels'
  | 'server.accounts' | 'server.invitations' | 'server.devices' | 'server.apiKeys' | 'server.memberTokens' | 'server.webhooks' | 'server.assistants'
  | 'server.metadataSettings'
  | 'server.transcodeStatus'
  | 'server.remoteStatus' | 'server.secureConnection' | 'server.accountConnection' | 'server.certificateStatus' | 'server.addresses' | 'server.connection' | 'server.ownCertificateStatus'
  | 'server.diskUsage' | 'server.retention' | 'server.remoteSources' | 'server.backups' | 'server.missingTitles'
  | 'server.windows' | 'server.jobs'
  | 'server.logs' | 'server.detailWindow' | 'server.records' | 'server.capabilities' | 'server.supportExport' | 'server.feedback' | 'server.stateFolder'
  | 'server.identity' | 'server.updates' | 'server.restart' | 'server.clientReports'
  | 'server.setAsideTitles' | 'server.backgroundPause' | 'server.ffmpegStatus' | 'server.resourceUsage' | 'server.storageStatus' | 'server.downloadUsage'
  | 'server.playbackProblems' | 'server.skippedParts'
  | 'server.segments';

type RowCopy = Readonly<{
  label: MessageId;
  help?: MessageId;
  /** Named on the row by `settingsScopeLabel` ("All TVs", "This device"). Profile settings say nothing: they are the rule. */
  scope?: SettingsScope;
  /** Shown only while another preference has this value (the countdown, while auto-play is on). */
  when?: Readonly<{key: string; equals: PreferenceValue}>;
  /** Keep a dependent control visible, but editable only for these parent values. */
  enableWhen?: Readonly<{key: string; in: readonly PreferenceValue[]}>;
}>;

export type SettingsRow =
  | (RowCopy & Readonly<{kind: 'preference'; key: string; control: SettingsControl}>)
  | (RowCopy & Readonly<{kind: 'composite'; id: CompositeId; /** `device`: kept by this device itself, not in the registry. */ storage: 'registry' | 'device'; control: Extract<SettingsControl, {type: 'choice' | 'switch'}>}>)
  | (RowCopy & Readonly<{kind: 'action'; id: SettingsActionId; icon?: IconId; destructive?: boolean; /** A drawn destination value; the action still runs the one target. */ value?: MessageId}>)
  | Readonly<{kind: 'custom'; id: SettingsCustomId; /** For search and for a client that titles the panel. */ label?: MessageId}>
  | ServerSettingRow;

/** How a server setting is edited. A number shows `unit` after it; `scale` converts what is shown to what is stored (minutes shown, milliseconds stored: 60000). */
export type ServerControl =
  | Readonly<{type: 'switch'}>
  | Readonly<{type: 'choice'; choices: readonly SettingsChoice[]}>
  /** Choices the server names at run time (network interfaces, what a recording can be converted to): resolved by `serverChoices`. */
  | Readonly<{type: 'choiceFrom'; source: 'interfaces' | 'conversionModes'}>
  | Readonly<{type: 'number'; min: number; max: number; unit?: MessageId; scale?: number; /** An empty field stores this (no limit) and shows the placeholder. */ empty?: Readonly<{stores: null | 0; placeholder: MessageId}>}>
  | Readonly<{type: 'text'; mono?: boolean; example?: string; maxLength?: number}>
  /** A list edited one entry per line (networks, addresses). */
  | Readonly<{type: 'lines'; example?: string}>;

export type ServerSettingRow = Readonly<{
  kind: 'server-setting';
  form: ServerFormId;
  /** Dotted path into the form's draft (`keepPolicy.mode`). A row whose value the server did not send is absent. */
  key: string;
  label: MessageId;
  help?: MessageId;
  control: ServerControl;
  /** Shown only while another value of the same form matches. */
  when?: Readonly<{key: string; equals?: unknown; not?: unknown}>;
  /** Visible but editable only while another key of the same form's draft holds one of these values; otherwise dimmed. */
  enableWhen?: Readonly<{key: string; in: readonly unknown[]}>;
}>;

export type SettingsSection = Readonly<{id: string; title?: MessageId; /** A sentence under the title. */ description?: MessageId; /** A quiet group below the rest ("Advanced"), collapsed until asked for. */ advanced?: boolean; /** The sub-page this section belongs to, on a page that has them. */ tab?: string; rows: readonly SettingsRow[]}>;
export type SettingsTab = Readonly<{id: string; title: MessageId}>;
export type SettingsPage = Readonly<{id: string; title: MessageId; icon: IconId; /** Sub-pages (Sources · Recording · Library Channels); each section names the one it is on. */ tabs?: readonly SettingsTab[]; sections: readonly SettingsSection[]}>;
export type SettingsHeading = Readonly<{id: SettingsHeadingId; title: MessageId; pages: readonly SettingsPage[]}>;
export type SettingsStructure = Readonly<{headings: readonly SettingsHeading[]}>;

/** Runtime engine-setting values; authored controls live in the platform Settings spec. */
export const PLAYER_ENGINES = ['automatic', 'native', 'mpv'] as const;
export type PlayerEngineSetting = (typeof PLAYER_ENGINES)[number];

// ── Authoring helpers ────────────────────────────────────────────────────────

type Only = Readonly<{platforms?: readonly SettingsPlatform[]; needs?: (c: SettingsCapabilities) => boolean}>;
type Authored<T> = T & Only;
/** `administrators`: an administrator (not only the owner) sees this section, because every call it makes is one the server allows that tier. */
type AuthoredSection = Readonly<{id: string; title?: MessageId; description?: MessageId; advanced?: boolean; tab?: string; administrators?: boolean; rows: readonly Authored<SettingsRow>[]}> & Only;
type AuthoredPage = Readonly<{id: string; title: MessageId; icon: IconId; tabs?: readonly SettingsTab[]; sections: readonly AuthoredSection[]}> & Only;

/** Durations are stored in milliseconds and shown in seconds. */
const seconds = (...values: number[]): SettingsChoice[] => values.map(value => ({value: value * 1000, label: 'pref.unit.seconds', labelValues: {count: value}}));
const speedChoice = (value: number): SettingsChoice => (value === 1 ? {value, label: 'pref.unit.speedNormal'} : {value, label: 'pref.unit.speed', labelValues: {value}});
const speeds: SettingsChoice[] = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2].map(speedChoice);
const audiobookSpeeds: SettingsChoice[] = [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3].map(speedChoice);
const skipChoices = (key: string): SettingsChoice[] => (['ask', 'auto', 'off'] as const).map(value => ({value, label: `pref.value.${key}.${value}` as MessageId}));
/** The saved default qualities (`quality.home`, `quality.remote`): the original, automatic, then the server's ladder. */
const qualityChoices: SettingsChoice[] = [
  {value: 'original', label: 'settings.quality.limit.original'},
  {value: 'automatic', label: 'settings.quality.limit.automatic'},
  {value: '2160p', label: 'pref.unit.4k'},
  ...(['1440', '1080', '720', '480', '360'] as const).map(value => ({value: value + 'p', label: 'pref.unit.height' as MessageId, labelValues: {value}})),
];
const pref = (key: string, label: MessageId, control: SettingsControl, more: Partial<RowCopy> & Only = {}): Authored<SettingsRow> => ({kind: 'preference', key, label, control, ...more});
const toggle = (key: string, label: MessageId, more: Partial<RowCopy> & Only = {}) => pref(key, label, {type: 'switch'}, more);
const custom = (id: SettingsCustomId, label?: MessageId, only: Only = {}): Authored<SettingsRow> => ({kind: 'custom', id, ...(label ? {label} : {}), ...only});

// ── Account ─────────────────────────────────────────────────────────────────

const accountPages: readonly AuthoredPage[] = [
  {
    id: 'profile', title: 'settings.page.profile', icon: 'profile', sections: [
      {id: 'who', rows: [custom('identity', 'settings.row.identity')]},
      // Direct-sign-in servers manage profiles, sessions and two-step here; a Portico Account manages its own on the account site.
      {id: 'profiles', title: 'settings.section.profiles', rows: [custom('profiles', 'settings.section.profiles')], needs: c => !!c.localAccount || !!c.hostedAccount},
      // Television hands typing-heavy tasks to a phone or computer; a profile that isn't the account's main one manages nothing.
      {id: 'signIn', title: 'settings.section.signIn', rows: [custom('password', 'settings.row.password'), custom('twoStep', 'settings.row.twoStep')], platforms: ['web', 'phone'], needs: c => c.manageSignIn ?? (!!c.localAccount && !c.porticoMember)},
      // What the Portico Account itself manages, and this server's link to it; a client with nothing to say draws nothing.
      {id: 'porticoAccount', rows: [custom('porticoAccount', 'settings.row.porticoAccount')]},
      {id: 'devices', title: 'settings.section.devices', rows: [custom('devices', 'settings.section.devices')], needs: c => c.manageDevices ?? !!c.localAccount},
      // Frame 28 puts the TV row in Appearance; touch and web keep it in Profile.
      {id: 'notifications', title: 'next.settingsSpec.account.profile.groups.7.title', platforms: ['web', 'phone'], rows: [
        toggle('notifications.badges', 'next.settingsSpec.account.profile.groups.7.rows.0.label', {help: 'next.settingsSpec.account.profile.groups.7.rows.0.help'}),
      ]},
      // For a member's own scripts and apps: kept out of the way until asked for. The owner makes API keys under Server › Integrations.
      {id: 'tokens', title: 'api.tokens.title', advanced: true, rows: [custom('accessTokens', 'api.tokens.title')], platforms: ['web', 'phone'], needs: c => !!c.accessTokens},
      {id: 'session', rows: [{kind: 'action', id: 'signOut', label: 'action.signOut', icon: 'signOut', destructive: true}]},
    ],
  },
  {
    id: 'playback', title: 'settings.page.playback', icon: 'play', sections: [
      {id: 'next', title: 'settings.section.nextEpisode', rows: [
        toggle('playback.autoplayNext', 'settings.row.autoplayNext', {help: 'pref.help.playback.autoplayNext'}),
        pref('playback.upNextCountdownMs', 'settings.row.countdown', {type: 'choice', choices: [{value: 0, label: 'pref.value.playback.upNextCountdownMs.0'}, ...seconds(5, 10, 15)]}, {when: {key: 'playback.autoplayNext', equals: true}}),
        toggle('playback.startNextDuringCredits', 'settings.row.startNextDuringCredits', {help: 'pref.help.playback.startNextDuringCredits', when: {key: 'playback.autoplayNext', equals: true}}),
        // TV frame 8 composes these existing preferences into one picker; Off
        // keeps the remembered count, so enabling it again needs no new default.
        {kind: 'composite', id: 'playback.stillWatching', storage: 'registry', platforms: ['tv'],
          needs: c => !c.preferenceKeys || c.preferenceKeys.has('playback.passoutProtection') && c.preferenceKeys.has('playback.passoutAfterEpisodes'),
          label: 'next.settingsSpec.account.playback.groups.0.rows.1.label',
          control: {type: 'choice', choices: [
            {value: '0', label: 'next.settingsSpec.account.playback.groups.0.rows.1.control.options.0.label'},
            {value: '2', label: 'next.settingsSpec.account.playback.groups.0.rows.1.control.options.1.label'},
            {value: '3', label: 'next.settingsSpec.account.playback.groups.0.rows.1.control.options.2.label'},
            {value: '4', label: 'next.settingsSpec.account.playback.groups.0.rows.1.control.options.3.label'},
            {value: '5', label: 'next.settingsSpec.account.playback.groups.0.rows.1.control.options.4.label'},
          ]}},
        toggle('playback.passoutProtection', 'settings.row.stillWatching', {help: 'settings.help.stillWatching', platforms: ['web', 'phone']}),
        pref('playback.passoutAfterEpisodes', 'settings.row.stillWatchingAfter', {type: 'choice', choices: [2, 3, 4, 5].map(value => ({value, label: 'pref.unit.episodes' as MessageId, labelValues: {count: value}}))}, {when: {key: 'playback.passoutProtection', equals: true}, platforms: ['web', 'phone']}),
      ]},
      {id: 'skipping', title: 'settings.section.skipping', rows: [
        pref('playback.introSkip', 'pref.label.playback.introSkip', {type: 'choice', choices: skipChoices('playback.introSkip')}),
        pref('playback.creditsSkip', 'pref.label.playback.creditsSkip', {type: 'choice', choices: skipChoices('playback.creditsSkip')}),
        pref('playback.recapSkip', 'pref.label.playback.recapSkip', {type: 'choice', choices: skipChoices('playback.recapSkip')}),
        pref('playback.skipBackMs', 'pref.label.playback.skipBackMs', {type: 'choice', choices: seconds(5, 10, 15, 30)}, {help: 'pref.help.playback.skipBackMs'}),
        pref('playback.skipForwardMs', 'pref.label.playback.skipForwardMs', {type: 'choice', choices: seconds(10, 15, 30, 60)}, {help: 'pref.help.playback.skipForwardMs'}),
      ]},
      // No music speed: a song plays at the speed it was recorded.
      {id: 'speed', title: 'settings.section.speed', needs: c => c.playbackSpeed !== false, rows: [
        pref('playback.defaultSpeed', 'settings.row.videoSpeed', {type: 'choice', choices: speeds}),
      ]},
      {id: 'advanced', title: 'settings.section.advanced', advanced: true, rows: [
        pref('playback.playedThresholdPercent', 'settings.row.watchedThreshold', {type: 'choice', choices: [80, 85, 90, 95, 100].map(value => ({value, label: 'pref.unit.percent' as MessageId, labelValues: {value}}))}, {help: 'pref.help.playback.playedThresholdPercent'}),
        // The other end of the same scale: how much is watched before a title joins Continue Watching (the server honours it).
        pref('playback.startedThresholdPercent', 'pref.label.playback.startedThresholdPercent', {type: 'choice', choices: [1, 2, 5, 10, 15, 20, 25].map(value => ({value, label: 'pref.unit.percent' as MessageId, labelValues: {value}}))}, {help: 'pref.help.playback.startedThresholdPercent'}),
      ]},
    ],
  },
  {
    id: 'languages', title: 'settings.page.audioSubtitles', icon: 'subtitles', sections: [
      {id: 'audio', title: 'settings.section.audio', rows: [
        pref('playback.preferredAudioLanguages', 'pref.label.playback.preferredAudioLanguages', {type: 'languages'}, {help: 'settings.help.audioLanguages'}),
      ]},
      {id: 'subtitles', title: 'settings.section.subtitles', rows: [
        pref('playback.subtitleMode', 'settings.row.subtitleMode', {type: 'choice', choices: [
          {value: 'off', label: 'settings.subtitleMode.off'}, {value: 'forced', label: 'settings.subtitleMode.forced'}, {value: 'always', label: 'settings.subtitleMode.always'},
        ]}, {help: 'settings.help.subtitleMode'}),
        pref('playback.preferredSubtitleLanguages', 'pref.label.playback.preferredSubtitleLanguages', {type: 'languages'}, {help: 'settings.help.subtitleLanguages'}),
      ]},
      // Every player engine draws captions in this style, on top of the device's own Accessibility caption settings; the preview is the web's.
      {id: 'appearance', title: 'settings.section.subtitleAppearance', rows: [
        custom('subtitlePreview', 'settings.section.subtitleAppearance', {platforms: ['web']}),
        pref('playback.subtitleSize', 'settings.row.subtitleSize', {type: 'choice', choices: (['small', 'medium', 'large', 'extra-large'] as const).map(value => ({value, label: `settings.subtitleSize.${value}` as MessageId}))}),
        pref('playback.subtitleBackground', 'settings.row.subtitleBackground', {type: 'choice', choices: (['none', 'translucent', 'opaque'] as const).map(value => ({value, label: `settings.subtitleBackground.${value}` as MessageId}))}),
      ]},
    ],
  },
  {
    id: 'quality', title: 'settings.page.quality', icon: 'quality', sections: [
      // Two saved defaults: at home and away. The player can still choose another quality for one title.
      {id: 'limits', title: 'settings.section.qualityLimits', rows: [
        pref('quality.home', 'settings.row.qualityHome', {type: 'choice', choices: qualityChoices}, {scope: 'deviceClass', help: 'settings.help.qualityHome'}),
        pref('quality.remote', 'settings.row.qualityAway', {type: 'choice', choices: qualityChoices}, {scope: 'deviceClass', help: 'settings.help.qualityAway'}),
      ]},
      {id: 'method', rows: [
        // One choice in place of three four-way policies; it writes a combination that makes sense.
        {kind: 'composite', id: 'playbackMethod', storage: 'registry', label: 'settings.row.playbackMethod', help: 'settings.help.playbackMethod', scope: 'deviceClass', control: {type: 'choice', choices: [
          {value: 'automatic', label: 'settings.playbackMethod.automatic'}, {value: 'original', label: 'settings.playbackMethod.original'}, {value: 'convert', label: 'settings.playbackMethod.convert'},
        ]}},
      ]},
      // Where the device cannot detect them, the viewer says what the TV shows and plays. Off: standard range and stereo.
      // One drawn choice, shown on a television. Its value is the viewer's override;
      // what the TV reports is read live and shown only until an override exists.
      {id: 'tvOutputs', title: 'next.settingsSpec.account.quality.groups.2.title', description: 'settings.section.tvOutputsHelp', platforms: ['tv'], rows: [
        {kind: 'composite', id: 'tvSupports', storage: 'device', label: 'next.settingsSpec.account.quality.groups.2.rows.0.label', help: 'next.settingsSpec.account.quality.groups.2.rows.0.help', scope: 'device', control: {type: 'choice', choices: [
          {value: 'none', label: 'next.settingsSpec.account.quality.groups.2.rows.0.control.options.0.label'},
          {value: 'hdr', label: 'next.settingsSpec.account.quality.groups.2.rows.0.control.options.1.label'},
          {value: 'surround', label: 'next.settingsSpec.account.quality.groups.2.rows.0.control.options.2.label'},
          {value: 'hdr-surround', label: 'next.settingsSpec.account.quality.groups.2.rows.0.control.options.3.label'},
        ]}},
      ]},
      // The short list for when detection is wrong. Each narrows what the server plans this device as.
      {id: 'fixes', title: 'settings.section.playbackFixes', description: 'settings.section.playbackFixesHelp', rows: [
        {kind: 'composite', id: 'forceStereo', storage: 'device', label: 'settings.row.forceStereo', help: 'settings.help.forceStereo', scope: 'device', control: {type: 'switch'}},
        {kind: 'composite', id: 'disableHdr', storage: 'device', label: 'settings.row.disableHdr', help: 'settings.help.disableHdr', scope: 'device', needs: c => c.hdr !== false, control: {type: 'switch'}},
      ]},
    ],
  },
  {
    id: 'music', title: 'settings.page.music', icon: 'music', sections: [
      {id: 'sound', title: 'next.settingsSpec.account.music.groups.0.title', rows: [
        pref('music.audioNormalization', 'settings.row.volumeLeveling', {type: 'choice', choices: (['off', 'track', 'album'] as const).map(value => ({value, label: `pref.value.music.audioNormalization.${value}` as MessageId}))}, {help: 'settings.help.volumeLeveling', needs: c => c.audioEffects !== false}),
        pref('music.crossfadeMs', 'pref.label.music.crossfadeMs', {type: 'choice', choices: [{value: 0, label: 'pref.value.music.crossfadeMs.0'}, ...seconds(2, 4, 6, 8, 10, 12)]}, {help: 'pref.help.music.crossfadeMs', needs: c => c.audioEffects !== false}),
        toggle('music.smoothTransitions', 'next.settingsSpec.account.music.groups.0.rows.1.children.0.label', {help: 'next.settingsSpec.account.music.groups.0.rows.1.children.0.help', enableWhen: {key: 'music.crossfadeMs', in: [2000, 4000, 6000, 8000, 10000, 12000]}, needs: c => c.audioEffects !== false}),
        toggle('music.gapless', 'pref.label.music.gapless', {help: 'settings.help.gapless', needs: c => c.audioEffects !== false}),
        custom('equalizer', 'next.settingsSpec.account.music.groups.0.rows.3.label', {platforms: ['phone', 'tv'], needs: c => c.equalizer === true}),
      ]},
      {id: 'lyrics', title: 'next.settingsSpec.account.music.groups.1.title', rows: [
        toggle('playback.showSyncedLyrics', 'settings.row.lyrics', {help: 'settings.help.lyrics'}),
      ]},
      {id: 'audiobooks', title: 'next.settingsSpec.account.music.groups.2.title', rows: [
        pref('audiobooks.defaultSpeed', 'next.settingsSpec.account.music.groups.2.rows.0.label', {type: 'choice', choices: audiobookSpeeds}, {needs: c => c.playbackSpeed !== false}),
        pref('audiobooks.skipBackMs', 'pref.label.playback.skipBackMs', {type: 'choice', choices: seconds(10, 15, 30, 60)}, {scope: 'profile'}),
        pref('audiobooks.skipForwardMs', 'pref.label.playback.skipForwardMs', {type: 'choice', choices: seconds(15, 30, 60, 90)}, {scope: 'profile'}),
        pref('audiobooks.resumeRewindMs', 'pref.label.audiobooks.resumeRewindMs', {type: 'choice', choices: [
          {value: 0, label: 'pref.value.audiobooks.resumeRewindMs.0'},
          ...[5, 15, 30].map(count => ({value: count * 1000, label: 'pref.value.audiobooks.resumeRewindMs.earlier' as MessageId, labelValues: {count}})),
        ]}, {scope: 'profile'}),
      ]},
      {id: 'quality', title: 'next.settingsSpec.account.music.groups.3.title', rows: [
        pref('music.qualityAway', 'pref.label.music.qualityAway', {type: 'choice', choices: [
          {value: 'original', label: 'next.settingsSpec.account.music.groups.3.rows.0.control.options.0.label'},
          {value: 'cd-quality', label: 'next.settingsSpec.account.music.groups.3.rows.0.control.options.1.label'},
          {value: '320', label: 'next.settingsSpec.account.music.groups.3.rows.0.control.options.2.label'},
          {value: '96', label: 'next.settingsSpec.account.music.groups.3.rows.0.control.options.3.label'},
        ]}, {scope: 'deviceClass'}),
        pref('music.qualityMobile', 'next.settingsSpec.account.music.groups.3.rows.1.label', {type: 'choice', choices: [
          {value: 'original', label: 'next.settingsSpec.account.music.groups.3.rows.1.control.options.0.label'},
          {value: 'cd-quality', label: 'next.settingsSpec.account.music.groups.3.rows.1.control.options.1.label'},
          {value: '320', label: 'next.settingsSpec.account.music.groups.3.rows.1.control.options.2.label'},
          {value: '96', label: 'next.settingsSpec.account.music.groups.3.rows.1.control.options.3.label'},
        ]}, {scope: 'deviceClass', platforms: ['phone']}),
        pref('music.qualityDownloads', 'next.settingsSpec.account.music.groups.3.rows.2.label', {type: 'choice', choices: [
          {value: 'original', label: 'next.settingsSpec.account.music.groups.3.rows.2.control.options.0.label'},
          {value: 'cd-quality', label: 'next.settingsSpec.account.music.groups.3.rows.2.control.options.1.label'},
          {value: '320', label: 'next.settingsSpec.account.music.groups.3.rows.2.control.options.2.label'},
          {value: '96', label: 'next.settingsSpec.account.music.groups.3.rows.2.control.options.3.label'},
        ]}, {scope: 'deviceClass', platforms: ['phone']}),
      ]},
      {id: 'advancedSound', title: 'next.settingsSpec.account.music.advanced.groups.0.title', advanced: true, rows: [
        // One device-local engine value, never an account preference (23 Oct ruling).
        {kind: 'composite', id: 'music.sampleRateMatching', storage: 'device', scope: 'device', platforms: ['phone', 'tv'],
          label: 'next.settingsSpec.account.music.advanced.groups.0.rows.0.label', help: 'next.settingsSpec.account.music.advanced.groups.0.rows.0.help',
          needs: c => c.sampleRateMatching === true, control: {type: 'choice', choices: [
            {value: 'smart', label: 'next.settingsSpec.account.music.advanced.groups.0.rows.0.control.options.0.label'},
            {value: 'strict', label: 'next.settingsSpec.account.music.advanced.groups.0.rows.0.control.options.1.label'},
            {value: 'off', label: 'next.settingsSpec.account.music.advanced.groups.0.rows.0.control.options.2.label'},
          ]}},
        toggle('music.neverBoost', 'next.settingsSpec.account.music.advanced.groups.0.rows.1.label', {help: 'next.settingsSpec.account.music.advanced.groups.0.rows.1.help', needs: c => c.audioEffects !== false}),
        pref('music.dsdGainDb', 'next.settingsSpec.account.music.advanced.groups.0.rows.2.label', {type: 'choice', choices: [0, -3, -6, -9, -12].map((value, index) => ({value, label: `next.settingsSpec.account.music.advanced.groups.0.rows.2.control.options.${index}.label` as MessageId}))}, {help: 'next.settingsSpec.account.music.advanced.groups.0.rows.2.help'}),
      ]},
    ],
  },
  // The reader's own defaults: the real `reading.*` and `comics.*` preferences the reader
  // already applies. Drawn from the current Settings specification; the page appears only
  // where the server publishes those keys.
  {
    id: 'reading', title: 'next.settingsSpec.account.reading.title', icon: 'book', platforms: ['web', 'phone'], sections: [
      {id: 'type', title: 'next.settingsSpec.account.reading.groups.0.title', rows: [
        pref('reading.theme', 'next.settingsSpec.account.reading.groups.0.rows.0.label', {type: 'choice', choices: [
          {value: 'dark', label: 'next.settingsSpec.account.reading.groups.0.rows.0.control.options.0.label'},
          {value: 'black', label: 'next.settingsSpec.account.reading.groups.0.rows.0.control.options.1.label'},
          {value: 'sepia', label: 'next.settingsSpec.account.reading.groups.0.rows.0.control.options.2.label'},
          {value: 'light', label: 'next.settingsSpec.account.reading.groups.0.rows.0.control.options.3.label'},
        ]}),
        pref('reading.font', 'next.settingsSpec.account.reading.groups.0.rows.1.label', {type: 'choice', choices: [
          {value: 'publisher', label: 'next.settingsSpec.account.reading.groups.0.rows.1.control.options.0.label'},
          {value: 'literata', label: 'next.settingsSpec.account.reading.groups.0.rows.1.control.options.1.label'},
          {value: 'manrope', label: 'next.settingsSpec.account.reading.groups.0.rows.1.control.options.2.label'},
        ]}),
        pref('reading.textSize', 'next.settingsSpec.account.reading.groups.0.rows.2.label', {type: 'choice', choices: [1, 2, 3, 4, 5, 6, 7].map(value => ({
          value, label: `next.settingsSpec.account.reading.groups.0.rows.2.control.options.${value - 1}.label` as MessageId,
        }))}),
      ]},
      {id: 'page', title: 'next.settingsSpec.account.reading.groups.1.title', rows: [
        pref('reading.lineSpacing', 'next.settingsSpec.account.reading.groups.1.rows.0.label', {type: 'choice', choices: [
          {value: 'tight', label: 'next.settingsSpec.account.reading.groups.1.rows.0.control.options.0.label'},
          {value: 'normal', label: 'next.settingsSpec.account.reading.groups.1.rows.0.control.options.1.label'},
          {value: 'loose', label: 'next.settingsSpec.account.reading.groups.1.rows.0.control.options.2.label'},
        ]}),
        pref('reading.margins', 'next.settingsSpec.account.reading.groups.1.rows.1.label', {type: 'choice', choices: [
          {value: 'narrow', label: 'next.settingsSpec.account.reading.groups.1.rows.1.control.options.0.label'},
          {value: 'normal', label: 'next.settingsSpec.account.reading.groups.1.rows.1.control.options.1.label'},
          {value: 'wide', label: 'next.settingsSpec.account.reading.groups.1.rows.1.control.options.2.label'},
        ]}),
        pref('reading.alignment', 'next.settingsSpec.account.reading.groups.1.rows.2.label', {type: 'choice', choices: [
          {value: 'left', label: 'next.settingsSpec.account.reading.groups.1.rows.2.control.options.0.label'},
          {value: 'justified', label: 'next.settingsSpec.account.reading.groups.1.rows.2.control.options.1.label'},
        ]}),
        pref('reading.layout', 'next.settingsSpec.account.reading.groups.1.rows.3.label', {type: 'choice', choices: [
          {value: 'pages', label: 'next.settingsSpec.account.reading.groups.1.rows.3.control.options.0.label'},
          {value: 'scroll', label: 'next.settingsSpec.account.reading.groups.1.rows.3.control.options.1.label'},
        ]}),
        toggle('reading.twoPages', 'next.settingsSpec.account.reading.groups.1.rows.4.label', {help: 'next.settingsSpec.account.reading.groups.1.rows.4.help'}),
      ]},
      {id: 'comics', title: 'next.settingsSpec.account.reading.groups.2.title', rows: [
        pref('comics.fit', 'next.settingsSpec.account.reading.groups.2.rows.0.label', {type: 'choice', choices: [
          {value: 'auto', label: 'next.settingsSpec.account.reading.groups.2.rows.0.control.options.0.label'},
          {value: 'width', label: 'next.settingsSpec.account.reading.groups.2.rows.0.control.options.1.label'},
          {value: 'height', label: 'next.settingsSpec.account.reading.groups.2.rows.0.control.options.2.label'},
        ]}),
        pref('comics.direction', 'next.settingsSpec.account.reading.groups.2.rows.1.label', {type: 'choice', choices: [
          {value: 'file', label: 'next.settingsSpec.account.reading.groups.2.rows.1.control.options.0.label'},
          {value: 'ltr', label: 'next.settingsSpec.account.reading.groups.2.rows.1.control.options.1.label'},
          {value: 'rtl', label: 'next.settingsSpec.account.reading.groups.2.rows.1.control.options.2.label'},
          {value: 'vertical', label: 'next.settingsSpec.account.reading.groups.2.rows.1.control.options.3.label'},
        ]}),
      ]},
      {id: 'advanced', title: 'next.settingsSpec.account.reading.advanced.groups.0.title', advanced: true, rows: [
        toggle('reading.tapToTurn', 'next.settingsSpec.account.reading.advanced.groups.0.rows.0.label', {help: 'next.settingsSpec.account.reading.advanced.groups.0.rows.0.help', platforms: ['phone']}),
        toggle('reading.hyphenation', 'next.settingsSpec.account.reading.advanced.groups.0.rows.1.label', {help: 'next.settingsSpec.account.reading.advanced.groups.0.rows.1.help'}),
        pref('reading.highlightColour', 'next.settingsSpec.account.reading.advanced.groups.0.rows.2.label', {type: 'choice', choices: [
          {value: 'yellow', label: 'next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.0.label'},
          {value: 'green', label: 'next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.1.label'},
          {value: 'blue', label: 'next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.2.label'},
          {value: 'pink', label: 'next.settingsSpec.account.reading.advanced.groups.0.rows.2.control.options.3.label'},
        ]}),
      ]},
      {id: 'advancedComics', title: 'next.settingsSpec.account.reading.advanced.groups.1.title', advanced: true, rows: [
        toggle('comics.firstPageAlone', 'next.settingsSpec.account.reading.advanced.groups.1.rows.0.label', {help: 'next.settingsSpec.account.reading.advanced.groups.1.rows.0.help', enableWhen: {key: 'reading.twoPages', in: [true]}}),
        pref('reading.pdfView', 'next.settingsSpec.account.reading.advanced.groups.1.rows.1.label', {type: 'choice', choices: [
          {value: 'automatic', label: 'next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.0.label'},
          {value: 'spread', label: 'next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.1.label'},
          {value: 'width', label: 'next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.2.label'},
          {value: 'page', label: 'next.settingsSpec.account.reading.advanced.groups.1.rows.1.control.options.3.label'},
        ]}),
      ]},
    ],
  },
  {
    id: 'appearance', title: 'settings.page.appearance', icon: 'sliders', sections: [
      {id: 'look', rows: [
        // The same customization Home draws at the end of its rows; this is the profile's own
        // layout, so it is a link to that one dialog, not a setting kept here.
        {kind: 'action', id: 'customizeHome', label: 'next.settingsSpec.account.appearance.groups.0.rows.0.label', help: 'next.settingsSpec.account.appearance.groups.0.rows.0.help', scope: 'profile'},
        // Every app draws cards at this size and backdrops behind titles (the shell applies both).
        {kind: 'composite', id: 'cardSize', storage: 'registry', label: 'settings.row.cardSize', scope: 'deviceClass', needs: c => !c.preferenceKeys || c.preferenceKeys.has('appearance.cardSizePercent'), control: {type: 'choice', choices: [{value: 'small', label: 'settings.cardSize.small'}, {value: 'medium', label: 'settings.cardSize.medium'}, {value: 'large', label: 'settings.cardSize.large'}]}},
        toggle('appearance.showBackdrops', 'pref.label.appearance.showBackdrops', {scope: 'deviceClass', help: 'settings.help.backdrops'}),
        // Apple keeps this on the device, beside the system's own Reduce Motion.
        {kind: 'composite', id: 'reduceMotion', storage: 'device', label: 'pref.label.appearance.reduceMotion', help: 'settings.help.reduceMotionDevice', scope: 'device', platforms: ['phone', 'tv'], control: {type: 'switch'}},
        {kind: 'composite', id: 'textSize', storage: 'device', label: 'settings.row.textSize', scope: 'device', platforms: ['tv'], control: {type: 'choice', choices: [{value: 'standard', label: 'settings.textSize.standard'}, {value: 'large', label: 'settings.textSize.large'}]}},
        // A box that does not say it is a television (or a tablet that is used as one) is laid out
        // the way the person says: Automatic decides from what the device reports.
        {kind: 'composite', id: 'layout', storage: 'device', label: 'settings.row.layout', help: 'settings.help.layout', scope: 'device', platforms: ['phone', 'tv'], needs: c => !!c.layoutChoice, control: {type: 'choice', choices: [{value: 'automatic', label: 'settings.layout.automatic'}, {value: 'television', label: 'settings.layout.television'}, {value: 'touch', label: 'settings.layout.touch'}]}},
        {kind: 'composite', id: 'libraryView', storage: 'device', label: 'settings.row.libraryView', help: 'settings.help.libraryView', scope: 'device', platforms: ['web'], control: {type: 'choice', choices: [{value: 'grid', label: 'settings.libraryView.grid'}, {value: 'list', label: 'settings.libraryView.list'}]}},
        // Frame 28 draws the rail profile badge choice here on television; the same preference
        // and copy, with touch and web keeping their Profile row.
        toggle('notifications.badges', 'next.settingsSpec.account.profile.groups.7.rows.0.label', {platforms: ['tv'], help: 'settings.help.unreadBadgeTV'}),
      ]},
    ],
  },
  {
    id: 'region', title: 'settings.page.region', icon: 'globe', sections: [
      {id: 'region', rows: [
        pref('region.locale', 'settings.row.language', {type: 'locale'}, {help: 'next.settingsSpec.account.language.groups.0.rows.0.help'}),
        // No time zone: every client shows times in the device's own zone (Justin, 5 Oct 2026).
        pref('region.hourCycle', 'pref.label.region.hourCycle', {type: 'choice', choices: (['auto', 'h12', 'h23'] as const).map(value => ({value, label: `pref.value.region.hourCycle.${value}` as MessageId}))}),
      ]},
    ],
  },
  {
    id: 'privacy', title: 'settings.page.privacy', icon: 'shield', sections: [
      // What pausing and clearing do not change, said before the switches that might suggest otherwise.
      {id: 'note', rows: [custom('privacyNote')]},
      {id: 'history', rows: [
        toggle('privacy.pauseWatchHistory', 'next.settingsSpec.account.privacy.advanced.groups.1.rows.0.label', {help: 'pref.help.privacy.pauseWatchHistory'}),
        toggle('privacy.showActivityToMembers', 'settings.row.showActivity', {help: 'settings.help.showActivity'}),
        // Off: in a Watch Together room this profile's activity is shown without its name.
        toggle('privacy.includeInWatchTogether', 'pref.label.privacy.includeInWatchTogether'),
        toggle('search.rememberHistory', 'pref.label.search.rememberHistory'),
      ]},
      {id: 'recommendations', rows: [{kind: 'action', id: 'resetRecommendations', label: 'settings.resetRecommendations', help: 'settings.resetRecommendations.help', icon: 'refresh'}]},
    ],
  },
  {
    id: 'device', title: 'settings.page.device', icon: 'tvDevice', sections: [
      {id: 'device', rows: [
        custom('deviceName', 'settings.row.deviceName', {needs: c => !!c.deviceName}),
        {kind: 'action', id: 'linkTV', label: 'settings.linkTV', help: 'settings.linkTVHelp', icon: 'tvDevice', platforms: ['web', 'phone']},
        {kind: 'composite', id: 'receiveFromDevices', storage: 'device', label: 'settings.row.receiveFromDevices', help: 'settings.help.receiveFromDevices', scope: 'device', platforms: ['tv'], control: {type: 'switch'}},
        custom('downloadsStorage', 'settings.row.downloadsStorage', {platforms: ['phone'], needs: c => !!c.downloads}),
      ]},
      // What this profile is playing on its other devices, to pause, resume or stop from here.
      {id: 'remote', rows: [custom('remoteControls', 'remote.title')]},
      {id: 'servers', title: 'settings.section.servers', rows: [custom('servers', 'settings.section.servers')]},
      // account-device.json: About Portico is the last group of This device, not a page of its own.
      // Frames 11 and 12's advanced Connection group. Status is the route in use: secure from its
      // scheme, at home from the server's own answer (`ConnectionDocument.remote`, P2-69). It sits
      // before About so the Player section inserted ahead of it leaves About as the last group.
      // Insecure connections is Plex's "Allow insecure connections" (Never / On my network / Always).
      {id: 'deviceConnection', title: 'next.settingsSpec.account.device.advanced.groups.4.title', advanced: true, platforms: ['tv', 'phone'], rows: [
        {kind: 'custom', id: 'deviceConnectionStatus', label: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.label'},
        {kind: 'custom', id: 'deviceServerAddress', label: 'next.settingsSpec.account.device.advanced.groups.4.rows.1.label'},
        {kind: 'custom', id: 'deviceTestConnection', label: 'next.settingsSpec.account.device.advanced.groups.4.rows.2.label'},
        {kind: 'custom', id: 'deviceInsecureConnections', label: 'next.settingsSpec.account.device.advanced.groups.4.rows.3.label'},
      ]},
      {id: 'about', title: 'settings.about', rows: [custom('about', 'settings.about')]},
    ],
  },
];

// ── Server (owners) ─────────────────────────────────────────────────────────
// A dozen pages in place of the console's fifteen sections; each duplicated concept has one home.
// Status is at the top of a page and settings below it. Plain settings are `server-setting`
// rows, written once for the web and the iPhone app; the rest are panels each client draws.
// Phones and tablets have the owner's administration as on the web (Justin, 14 Oct 2026);
// television shows the read-only Dashboard only (who is watching, alerts, health).
// An administrator sees the sections marked `administrators`: what the server lets that tier do
// (who is watching and their transport, people below them, invitations, devices, logs).

const TV_SERVER_PAGES = new Set(['dashboard']);
const panel = (id: string, panelId: SettingsCustomId, title?: MessageId, more: Only & {tab?: string; description?: MessageId; administrators?: boolean} = {}): AuthoredSection => ({id, ...(title ? {title} : {}), rows: [custom(panelId, title)], ...more});
const setting = (form: ServerFormId, key: string, label: MessageId, control: ServerControl, more: {help?: MessageId; when?: ServerSettingRow['when']; enableWhen?: ServerSettingRow['enableWhen']} = {}): Authored<SettingsRow> => ({kind: 'server-setting', form, key, label, control, ...more});
const on = (form: ServerFormId, key: string, label: MessageId, help?: MessageId, when?: ServerSettingRow['when']) => setting(form, key, label, {type: 'switch'}, {...(help ? {help} : {}), ...(when ? {when} : {})});
const choices = (prefix: string, values: readonly string[]): SettingsChoice[] => values.map(value => ({value, label: `${prefix}.${value}` as MessageId}));
const unlimited = {stores: null, placeholder: 'settings.server.unlimited'} as const;
const noLimit = {stores: 0, placeholder: 'settings.server.unlimited'} as const;
const PAUSED_MINUTES = [0, 15, 30, 60, 120, 240];

const serverPages: readonly AuthoredPage[] = [
  {id: 'dashboard', title: 'settings.server.dashboard', icon: 'pulse', sections: [
    panel('nowPlaying', 'server.nowPlaying', 'settings.server.nowPlaying', {administrators: true}),
    panel('alerts', 'server.alerts', 'settings.server.alerts'),
    panel('health', 'server.health', 'settings.server.health'),
    panel('activity', 'server.activity', 'settings.server.activity'),
    panel('statistics', 'server.statistics', 'settings.server.statistics', {platforms: ['web', 'phone']}),
  ]},
  // Every play on the server, with filters: the owner's record (a viewer's own History is under Saved).
  {id: 'history', title: 'settings.server.playHistory', icon: 'clock', sections: [panel('plays', 'server.playHistory')]},
  {id: 'libraries', title: 'settings.server.libraries', icon: 'library', sections: [panel('libraries', 'server.libraries')]},
  // What every library shares when it looks titles up: the owner's own provider keys and how much local artwork is kept.
  // Which provider a library uses is that library's own setting (Libraries › a library › Metadata).
  {id: 'metadata', title: 'settings.server.metadata', icon: 'image', platforms: ['web', 'phone'], sections: [panel('settings', 'server.metadataSettings', 'settings.server.metadata')]},
  // Intro and credits detection: the server's mode on every owner client; its progress and the
  // owner's review of what it found, by show and season, on the web.
  {id: 'segments', title: 'settings.server.segments', icon: 'next', platforms: ['web', 'phone'], sections: [
    {id: 'mode', rows: [
      setting('runtime', 'library.segmentDetection', 'settings.server.segmentMode', {type: 'choice', choices: choices('settings.server.segmentMode', ['standard', 'thorough', 'off'])}, {help: 'settings.server.segmentModeHelp'}),
    ]},
    panel('review', 'server.segments', undefined, {platforms: ['web', 'phone']}),
  ]},
  {id: 'live', title: 'settings.server.live', icon: 'live', needs: c => c.liveTV !== false,
    tabs: [{id: 'sources', title: 'settings.server.liveSources'}, {id: 'recording', title: 'settings.server.recording'}, {id: 'channels', title: 'settings.server.libraryChannels'}],
    sections: [
      panel('sources', 'server.liveSources', undefined, {tab: 'sources'}),
      {id: 'liveDefaults', tab: 'sources', title: 'web.dvrSettings.liveDefaults', description: 'web.dvrSettings.liveDefaultsLede', rows: [
        setting('liveDefaults', 'streamBufferMs', 'web.dvrSettings.buffer', {type: 'number', min: 0, max: 120, unit: 'settings.unit.seconds', scale: 1000}, {help: 'web.dvrSettings.bufferHelp'}),
        setting('liveDefaults', 'retryWindowMs', 'web.dvrSettings.retryWindow', {type: 'number', min: 5, max: 600, unit: 'settings.unit.seconds', scale: 1000}, {help: 'web.dvrSettings.retryWindowHelp'}),
        setting('liveDefaults', 'guideDays', 'web.dvrSettings.guideDays', {type: 'number', min: 1, max: 21, unit: 'settings.unit.days'}, {help: 'web.dvrSettings.guideDaysHelp'}),
        // The closed rewind window, shown only when the server sends its choice: an older
        // server that omits it keeps the row absent instead of a guessed 30-minute default.
        setting('liveDefaults', 'timeshiftMinutes', 'next.settingsSpec.server.live.groups.1.rows.2.label', {type: 'choice', choices: [
          {value: 10, label: 'next.settingsSpec.server.live.groups.1.rows.2.control.options.0.label'},
          {value: 30, label: 'next.settingsSpec.server.live.groups.1.rows.2.control.options.1.label'},
          {value: 60, label: 'next.settingsSpec.server.live.groups.1.rows.2.control.options.2.label'},
          {value: 120, label: 'next.settingsSpec.server.live.groups.1.rows.2.control.options.3.label'},
        ]}, {help: 'next.settingsSpec.server.live.groups.1.rows.2.help'}),
        setting('liveDefaults', 'userAgent', 'web.dvrSettings.userAgent', {type: 'text', mono: true}, {help: 'web.dvrSettings.userAgentHelp'}),
        on('liveDefaults', 'logoImport', 'web.dvrSettings.logoImport'),
        on('liveDefaults', 'discoveryEnabled', 'web.dvrSettings.discovery', 'web.dvrSettings.discoveryHelp'),
      ]},
      // The most one playlist or guide may bring in: plain numbers the owner can raise or lower.
      {id: 'liveLimits', tab: 'sources', title: 'server.live.limits', description: 'server.live.limitsLede', rows: [
        setting('liveDefaults', 'importLimits.channels', 'server.live.limitChannels', {type: 'number', min: 1, max: 100000}),
        setting('liveDefaults', 'importLimits.programmes', 'server.live.limitProgrammes', {type: 'number', min: 1, max: 10000000}),
        setting('liveDefaults', 'importLimits.bytes', 'server.live.limitSize', {type: 'number', min: 1, max: 1024, unit: 'settings.unit.mb', scale: 1048576}),
      ]},
      panel('tuners', 'server.tuners', undefined, {tab: 'recording'}),
      // Who may record, and the standing rules.
      panel('recordingRules', 'server.recordingRules', undefined, {tab: 'recording'}),
      // Only what the server honours: the rows that said "not available on this server" are gone.
      {id: 'recordingDefaults', tab: 'recording', title: 'web.dvrSettings.defaults', description: 'web.dvrSettings.defaultsLede', rows: [
        setting('recording', 'prePaddingMs', 'web.dvr.startEarly', {type: 'number', min: 0, max: 30, unit: 'settings.unit.minutes', scale: 60000}, {help: 'web.dvrSettings.startEarlyHelp'}),
        setting('recording', 'postPaddingMs', 'web.dvrSettings.finishLate', {type: 'number', min: 0, max: 120, unit: 'settings.unit.minutes', scale: 60000}, {help: 'web.dvrSettings.finishLateHelp'}),
        // Justin (15 Oct): live viewing keeps its tuner unless the owner says otherwise.
        setting('recording', 'liveTunerConflict', 'server.live.tunerConflict', {type: 'choice', choices: [{value: 'keep-watching', label: 'server.live.tunerConflict.keep'}, {value: 'stop-watching', label: 'server.live.tunerConflict.stop'}]}, {help: 'server.live.tunerConflictHelp'}),
        setting('recording', 'keepPolicy.mode', 'web.dvr.keepLabel', {type: 'choice', choices: [{value: 'keep-all', label: 'web.logs.everything'}, {value: 'keep-count', label: 'web.dvrSettings.keepNewest'}, {value: 'keep-days', label: 'web.dvrSettings.keepDays'}]}),
        setting('recording', 'keepPolicy.keepCount', 'web.dvrSettings.howMany', {type: 'number', min: 1, max: 1000}, {when: {key: 'keepPolicy.mode', equals: 'keep-count'}}),
        setting('recording', 'keepPolicy.keepDays', 'web.dvrSettings.howManyDays', {type: 'number', min: 1, max: 3650, unit: 'settings.unit.days'}, {when: {key: 'keepPolicy.mode', equals: 'keep-days'}}),
        setting('recording', 'conversion.mode', 'web.dvrSettings.afterRecording', {type: 'choiceFrom', source: 'conversionModes'}),
        on('recording', 'conversion.deleteOriginal', 'web.dvrSettings.removeOriginal', undefined, {key: 'conversion.mode', not: 'none'}),
        setting('recording', 'folderTemplate', 'web.dvrSettings.folderShows', {type: 'text', mono: true}),
        setting('recording', 'movieFolderTemplate', 'web.dvrSettings.folderMovies', {type: 'text', mono: true}),
      ]},
      panel('recordingStorage', 'server.recordingStorage', undefined, {tab: 'recording'}),
      panel('channels', 'server.channels', undefined, {tab: 'channels'}),
    ]},
  {id: 'people', title: 'settings.server.people', icon: 'people',
    tabs: [{id: 'accounts', title: 'settings.server.accounts'}, {id: 'invitations', title: 'settings.server.invitations'}, {id: 'devices', title: 'settings.section.devices'}],
    sections: [
      panel('accounts', 'server.accounts', undefined, {tab: 'accounts', administrators: true}),
      panel('invitations', 'server.invitations', undefined, {tab: 'invitations', administrators: true}),
      // Holding new devices until the owner approves them, and every device that has signed in, newest first.
      panel('devices', 'server.devices', 'settings.section.devices', {tab: 'devices', platforms: ['web', 'phone'], administrators: true}),
    ]},
  // Everything another program uses to work with this server, in one place: the owner's keys, members' own tokens,
  // messages sent out when something happens, and the address an assistant connects to.
  {id: 'integrations', title: 'settings.server.integrations', icon: 'link', platforms: ['web', 'phone'],
    tabs: [{id: 'keys', title: 'settings.server.apiKeys'}, {id: 'tokens', title: 'settings.server.memberTokens'}, {id: 'webhooks', title: 'api.webhooks.title'}, {id: 'assistants', title: 'settings.server.assistants'}],
    sections: [
      panel('apiKeys', 'server.apiKeys', 'settings.server.apiKeys', {tab: 'keys'}),
      {id: 'apiLimits', tab: 'keys', title: 'settings.server.apiLimits', rows: [
        setting('runtime', 'apiRequestsPerMinute', 'settings.server.apiRate', {type: 'number', min: 1, max: 100000, unit: 'settings.unit.perMinute', empty: {stores: 0, placeholder: 'settings.server.apiRateUsual'}}, {help: 'settings.server.apiRateHelp'}),
      ]},
      panel('webhooks', 'server.webhooks', 'api.webhooks.title', {tab: 'webhooks'}),
      panel('assistants', 'server.assistants', 'settings.server.assistants', {tab: 'assistants'}),
    ]},
  {id: 'streaming', title: 'settings.server.playback', icon: 'transcode', sections: [
    panel('status', 'server.transcodeStatus'),
    {id: 'away', title: 'next.settingsSpec.server.streaming.groups.0.title', rows: [
      setting('runtime', 'musicAwayQuality', 'next.settingsSpec.server.streaming.groups.0.rows.1.label', {type: 'choice', choices: [
        {value: 'original', label: 'next.settingsSpec.server.streaming.groups.0.rows.1.control.options.0.label'},
        {value: 'cd-quality', label: 'next.settingsSpec.server.streaming.groups.0.rows.1.control.options.1.label'},
        {value: '320', label: 'next.settingsSpec.server.streaming.groups.0.rows.1.control.options.2.label'},
        {value: '96', label: 'next.settingsSpec.server.streaming.groups.0.rows.1.control.options.3.label'},
      ]}),
    ]},
    {id: 'transcoding', title: 'settings.server.transcoding', description: 'web.transcoding.conversionLede', rows: [
      on('runtime', 'transcodingEnabled', 'settings.server.allowConverting', 'web.transcoding.allowTranscodingHelp'),
      setting('runtime', 'hardwareBackend', 'settings.server.hardware', {type: 'choice', choices: choices('settings.server.hardware', ['auto', 'software', 'videotoolbox', 'vaapi', 'qsv', 'nvenc', 'amf'])}, {help: 'settings.server.hardwareHelp'}),
      setting('runtime', 'hardwareDevice', 'settings.server.hardwareDevice', {type: 'text', mono: true, example: '/dev/dri/renderD128'}, {help: 'settings.server.hardwareDeviceHelp', when: {key: 'hardwareBackend', not: 'software'}}),
      setting('runtime', 'x264Preset', 'settings.server.encoderSpeed', {type: 'choice', choices: choices('settings.server.encoderSpeed', ['ultrafast', 'superfast', 'veryfast', 'faster', 'fast', 'medium', 'slow'])}, {help: 'settings.server.encoderSpeedHelp'}),
      on('runtime', 'hdrToneMapping', 'settings.server.toneMapping', 'settings.server.toneMappingHelp'),
      setting('runtime', 'hdrToneMappingAlgorithm', 'settings.server.toneMappingMethod', {type: 'choice', choices: choices('settings.server.toneMappingMethod', ['hable', 'mobius', 'reinhard', 'gamma', 'linear', 'clip'])}, {when: {key: 'hdrToneMapping', equals: true}}),
      on('runtime', 'directStreamRemux', 'settings.server.repackage', 'settings.server.repackageHelp'),
      setting('runtime', 'planningPolicy', 'settings.server.planning', {type: 'choice', choices: choices('settings.server.planning', ['maximum_fidelity', 'maximum_compatibility', 'minimize_server_work'])}, {help: 'settings.server.planningHelp'}),
      setting('runtime', 'temporaryDirectory', 'settings.server.tempFolder', {type: 'text', mono: true}, {help: 'settings.server.tempFolderHelp'}),
      // Shown in GB; stored in MiB.
      setting('runtime', 'sessionStorageMiB', 'settings.server.sessionSpace', {type: 'number', min: 1, max: 1024, unit: 'settings.unit.gb', scale: 1024}, {help: 'settings.server.sessionSpaceHelp'}),
      setting('runtime', 'ffmpegPath', 'settings.server.ffmpegPath', {type: 'text', mono: true, example: '/usr/local/bin/ffmpeg'}, {help: 'settings.server.ffmpegChosenHelp'}),
      custom('server.ffmpegStatus'),
    ]},
    {id: 'capacity', title: 'web.transcoding.capacity', description: 'web.transcoding.capacityLede', rows: [
      setting('runtime', 'serverCap', 'web.transcoding.serverCap', {type: 'number', min: 1, max: 1000000, empty: unlimited}, {help: 'web.transcoding.serverCapHelp'}),
      setting('runtime', 'perAccountCap', 'web.transcoding.accountCap', {type: 'number', min: 1, max: 1000000, empty: unlimited}, {help: 'settings.server.accountCapHelp'}),
      setting('runtime', 'maxConcurrentSessions', 'settings.server.conversions', {type: 'number', min: 0, max: 64, empty: noLimit}, {help: 'settings.server.conversionsHelp'}),
      setting('runtime', 'maxHardwareSessions', 'settings.server.conversionsHardware', {type: 'number', min: 0, max: 64, empty: noLimit}),
      setting('runtime', 'maxSoftwareSessions', 'settings.server.conversionsSoftware', {type: 'number', min: 0, max: 64, empty: noLimit}),
      setting('runtime', 'maxBackgroundSessions', 'settings.server.conversionsBackground', {type: 'number', min: 0, max: 64, empty: noLimit}, {help: 'settings.server.conversionsBackgroundHelp'}),
    ]},
    // Filed by what they are for: these three sat under "Access from outside".
    {id: 'limits', title: 'settings.server.streamingLimits', rows: [
      setting('connectivity', 'remoteBitrateLimitKbps', 'web.connectivity.qualityLimit', {type: 'number', min: 0, max: 200, unit: 'settings.unit.mbps', scale: 1000, empty: noLimit}, {help: 'settings.server.qualityLimitHelp'}),
      setting('connectivity', 'uploadCapacityKbps', 'web.connectivity.uploadSpeed', {type: 'number', min: 0, max: 10000, unit: 'settings.unit.mbps', scale: 1000, empty: noLimit}, {help: 'settings.server.uploadSpeedHelp'}),
      setting('connectivity', 'pausedSessionTimeoutMinutes', 'web.connectivity.pausedLimit', {type: 'choice', choices: PAUSED_MINUTES.map(value => ({value, label: (value === 0 ? 'web.connectivity.pausedNever' : value === 60 ? 'web.connectivity.pausedHour' : value % 60 === 0 ? 'web.connectivity.pausedHours' : 'web.connectivity.pausedMinutes') as MessageId, labelValues: {count: value % 60 === 0 ? value / 60 : value}}))}, {help: 'web.connectivity.pausedLimitHelp'}),
    ]},
    // Ways of playing a file that failed on a device and are being avoided for a while; absent while there are none.
    panel('problems', 'server.playbackProblems', 'server.routes.title', {platforms: ['web', 'phone']}),
  ]},
  {id: 'remote', title: 'settings.server.remote', icon: 'network', sections: [
    panel('status', 'server.remoteStatus'),
    // Secure connection (Justin, 15 Oct): status, what it does, on/off and the owner's own
    // certificate, in plain words. With a Portico Account it is on from the start; without one
    // the status line says how to turn it on. The switch appears once there is a connection to
    // switch (`claimed`, derived by the certificate form).
    {id: 'secure', title: 'settings.server.secure.title', description: 'settings.server.secure.lede', rows: [
      custom('server.secureConnection', 'settings.server.secure.title'),
      on('certificate', 'enabled', 'settings.server.secure.toggle', 'settings.server.secure.toggleHelp', {key: 'claimed', equals: true}),
      custom('server.certificateStatus'),
    ]},
    // The drawn Your own address group: the switch, then exactly what it needs. Every server
    // has it, whatever its sign-in; on a Portico Account server it is an extra route.
    {id: 'ownCertificate', title: 'next.settingsSpec.server.remote.groups.1.title', rows: [
      setting('connectivity', 'ownAddressEnabled', 'next.settingsSpec.server.remote.groups.1.rows.0.label', {type: 'switch'}, {help: 'next.settingsSpec.server.remote.groups.1.rows.0.help'}),
      setting('connectivity', 'customCertificateDomain', 'next.settingsSpec.server.remote.groups.1.rows.0.children.0.label', {type: 'text', mono: true, example: 'https://media.example.com'}, {help: 'next.settingsSpec.server.remote.groups.1.rows.0.children.0.help', enableWhen: {key: 'ownAddressEnabled', in: [true]}}),
      setting('connectivity', 'customCertificatePath', 'next.settingsSpec.server.remote.groups.1.rows.0.children.1.label', {type: 'text', mono: true, example: '/config/certs/fullchain.pem'}, {help: 'next.settingsSpec.server.remote.groups.1.rows.0.children.1.help', enableWhen: {key: 'ownAddressEnabled', in: [true]}}),
      setting('connectivity', 'customCertificateKeyPath', 'next.settingsSpec.server.remote.groups.1.rows.0.children.2.label', {type: 'text', mono: true, example: '/config/certs/privkey.pem'}, {help: 'next.settingsSpec.server.remote.groups.1.rows.0.children.2.help', enableWhen: {key: 'ownAddressEnabled', in: [true]}}),
      custom('server.ownCertificateStatus', 'next.settingsSpec.server.remote.groups.1.rows.0.children.3.label'),
    ]},
    panel('account', 'server.accountConnection', 'web.general.accountConnection'),
    {id: 'reach', title: 'web.network.remoteTitle', description: 'web.network.remoteLede', rows: [
      on('remote', 'enabled', 'web.network.remoteTitle', 'web.network.remoteHelp'),
      on('remote', 'ipv6Open', 'web.network.ipv6', 'web.network.ipv6Help'),
    ]},
    // The one list that decides home or away for every device, with where the owner is connected from right now.
    {id: 'home', title: 'settings.server.homeNetwork', description: 'settings.server.homeNetworkLede', rows: [
      custom('server.connection', 'settings.server.yourConnection'),
      setting('connectivity', 'lanNetworks', 'web.connectivity.homeNetworks', {type: 'lines', example: '192.168.2.0/24'}, {help: 'settings.server.homeNetworksHelp'}),
      on('connectivity', 'lanDiscoveryEnabled', 'web.connectivity.discovery', 'settings.server.discoveryHelp'),
      on('connectivity', 'dlnaEnabled', 'settings.server.dlna', 'settings.server.dlnaHelp'),
      custom('server.addresses'),
    ]},
    {id: 'outside', title: 'web.connectivity.outside', rows: [
      setting('connectivity', 'remoteSignInPolicy', 'next.settingsSpec.server.remote.groups.2.rows.0.label', {type: 'choice', choices: [
        {value: 'allow', label: 'next.settingsSpec.server.remote.groups.2.rows.0.control.options.0.label'},
        {value: 'owner-only', label: 'next.settingsSpec.server.remote.groups.2.rows.0.control.options.1.label'},
        {value: 'off', label: 'next.settingsSpec.server.remote.groups.2.rows.0.control.options.2.label'},
      ]}, {help: 'next.settingsSpec.server.remote.groups.2.rows.0.help'}),
      setting('connectivity', 'advertiseUrl', 'web.connectivity.advertiseUrl', {type: 'text', mono: true, example: 'https://media.example.com', maxLength: 300}, {help: 'settings.server.advertiseUrlHelp'}),
      setting('connectivity', 'accessUrls', 'web.connectivity.extraAddresses', {type: 'lines', example: 'https://media.example.com'}, {help: 'settings.server.extraAddressesHelp'}),
      setting('connectivity', 'trustedProxies', 'web.connectivity.trustedProxies', {type: 'lines', example: '127.0.0.1'}, {help: 'settings.server.trustedProxiesHelp'}),
    ]},
    {id: 'advanced', title: 'settings.section.advanced', advanced: true, rows: [
      setting('certificate', 'publicPort', 'settings.server.secure.port', {type: 'number', min: 1, max: 65535}, {help: 'settings.server.secure.portHelp', when: {key: 'claimed', equals: true}}),
      on('remote', 'mapping', 'web.network.mappingTitle', 'web.network.mappingHelp'),
      on('remote', 'pcp', 'settings.server.mapping.pcp', undefined, {key: 'mapping', equals: true}),
      on('remote', 'natpmp', 'settings.server.mapping.natpmp', undefined, {key: 'mapping', equals: true}),
      on('remote', 'upnp', 'settings.server.mapping.upnp', undefined, {key: 'mapping', equals: true}),
      setting('remote', 'publicPort', 'web.network.publicPort', {type: 'number', min: 1, max: 65535}),
      setting('remote', 'gateway', 'web.network.gateway', {type: 'text', mono: true, example: '192.168.1.1'}, {help: 'web.network.gatewayHelp'}),
      setting('connectivity', 'advertisedInterface', 'web.connectivity.interface', {type: 'choiceFrom', source: 'interfaces'}, {help: 'web.connectivity.interfaceHelp'}),
      // An empty field is the server's own limit, worked out from its memory; a change waits for a restart.
      setting('connectivity', 'connectionLimit', 'web.connectivity.connectionLimit', {type: 'number', min: 1, max: 65536, empty: {stores: 0, placeholder: 'settings.server.automatic'}}, {help: 'settings.server.connectionLimitHelp'}),
    ]},
  ]},
  // The current Settings specification gives remote sources their own Server page.
  // Native mounts its supported source editor here; the older Storage slot remains
  // available to existing web consumers until their separate migration.
  {id: 'remote-sources', title: 'settings.server.remoteSources', icon: 'globe', platforms: ['phone'], sections: [
    panel('sources', 'server.remoteSources', undefined, {platforms: ['phone']}),
  ]},
  {id: 'storage', title: 'settings.server.storage', icon: 'storage', sections: [
    panel('disk', 'server.diskUsage', 'settings.server.diskUsage'),
    {id: 'limits', rows: [
      setting('runtime', 'analysisStorageGiB', 'settings.server.analysisStorage', {type: 'number', min: 1, max: 16384}),
      setting('runtime', 'analysisStageStorageMiB', 'settings.server.analysisStageStorage', {type: 'number', min: 1, max: 4096}),
      setting('runtime', 'subtitleTrackLimit', 'settings.server.subtitleTracks', {type: 'number', min: 32, max: 4096}),
      setting('runtime', 'subtitleFileLimitMiB', 'settings.server.subtitleFileSize', {type: 'number', min: 32, max: 512}),
    ]},
    // Retention in one place: it was split between Maintenance and Logs & diagnostics.
    {id: 'retention', title: 'settings.server.retention', description: 'settings.server.retentionLede', rows: [
      setting('runtime', 'diagnosticDays', 'web.logs.includeServer', {type: 'number', min: 1, max: 30, unit: 'settings.unit.days'}, {help: 'web.logs.serverRecordsHelp'}),
      setting('runtime', 'notificationDays', 'web.logs.retentionNotifications', {type: 'number', min: 1, max: 180, unit: 'settings.unit.days'}, {help: 'web.logs.retentionNotificationsHelp'}),
      // Kept in full unless the owner asks for less: an empty field is "forever".
      setting('runtime', 'playHistoryDays', 'settings.server.playHistoryKeep', {type: 'number', min: 1, max: 36500, unit: 'settings.unit.days', empty: {stores: 0, placeholder: 'settings.server.keepForever'}}, {help: 'settings.server.playHistoryKeepHelp'}),
      setting('runtime', 'jobDays', 'web.logs.retentionTasks', {type: 'number', min: 1, max: 30, unit: 'settings.unit.days'}, {help: 'web.logs.retentionTasksHelp'}),
      setting('maintenance', 'backupKeepCount', 'web.maintenance.backupsToKeep', {type: 'number', min: 1, max: 365}),
      custom('server.retention'),
    ]},
    // Where the server keeps things, what is in use against the limits, and what downloads take, person by person.
    panel('usage', 'server.resourceUsage', 'server.usage.title', {platforms: ['web', 'phone']}),
    panel('places', 'server.storageStatus', 'server.places.title', {platforms: ['web', 'phone']}),
    panel('downloadUsage', 'server.downloadUsage', 'server.downloadUsage.title', {platforms: ['web', 'phone']}),
    panel('sources', 'server.remoteSources', 'settings.server.remoteSources'),
    panel('backups', 'server.backups', 'settings.server.backups'),
    {id: 'missing', title: 'settings.server.missingTitles', description: 'settings.server.missingTitlesLede', rows: [
      setting('runtime', 'library.missingTitles', 'settings.server.missingTitlesRule', {type: 'choice', choices: choices('settings.server.missingTitlesRule', ['immediately', 'after_days', 'never'])}, {help: 'settings.server.missingTitlesRuleHelp'}),
      setting('runtime', 'library.missingTitlesDays', 'settings.server.missingTitlesDays', {type: 'number', min: 1, max: 3650, unit: 'settings.unit.days'}, {when: {key: 'library.missingTitles', equals: 'after_days'}}),
      custom('server.missingTitles'),
    ]},
  ]},
  {id: 'schedule', title: 'settings.server.schedule', icon: 'calendar', sections: [
    panel('jobs', 'server.jobs', 'settings.server.jobs'),
    panel('windows', 'server.windows', 'settings.server.windows', {description: 'settings.server.windowsLede'}),
    {id: 'priority', rows: [
      // One named choice in place of rates and worker counts: how hard matching and artwork work.
      setting('maintenance', 'backgroundSpeed', 'metadata.settings.backgroundSpeed', {type: 'choice', choices: [{value: 'gentle', label: 'metadata.settings.gentle'}, {value: 'normal', label: 'metadata.settings.normal'}, {value: 'fast', label: 'metadata.settings.fast'}]}, {help: 'settings.server.backgroundSpeedHelp'}),
      custom('server.backgroundPause', 'settings.server.pauseBackground'),
    ]},
  ]},
  {id: 'troubleshooting', title: 'settings.server.troubleshooting', icon: 'wrench', sections: [
    // One control for more detail, in place of three.
    {id: 'detail', rows: [custom('server.detailWindow', 'settings.server.detailWindow')]},
    panel('logs', 'server.logs', 'settings.server.logs', {administrators: true}),
    panel('records', 'server.records', 'settings.server.records'),
    panel('capabilities', 'server.capabilities', 'settings.server.capabilities'),
    // Logs an app sent after something went wrong: to read, download and delete.
    // Titles a rebuild could not finish, with why: present only while there are any.
    panel('setAside', 'server.setAsideTitles', 'server.setAside.title', {platforms: ['web', 'phone'], administrators: true}),
    panel('clientReports', 'server.clientReports', 'web.diagnostics.clientReports', {platforms: ['web', 'phone']}),
    // What this app left out of the server's answers (a newer server, usually), on this device.
    panel('skipped', 'server.skippedParts', 'settings.server.skippedParts', {platforms: ['web', 'phone'], description: 'settings.server.skippedPartsLede'}),
    panel('export', 'server.supportExport', 'settings.server.supportExport'),
    panel('feedback', 'server.feedback', 'settings.server.feedback'),
    panel('state', 'server.stateFolder'),
  ]},
  {id: 'general', title: 'settings.server.general', icon: 'server', sections: [
    {id: 'identity', title: 'next.settingsSpec.server.general.groups.0.title', rows: [
      setting('runtime', 'name', 'web.general.serverName', {type: 'text', maxLength: 100}, {help: 'next.settingsSpec.server.general.groups.0.rows.0.help'}),
      custom('server.identity'),
    ]},
    panel('updates', 'server.updates', 'settings.server.updates'),
    panel('restart', 'server.restart', 'web.maintenance.serverRestart', {platforms: ['web', 'phone']}),
    {id: 'alerts', title: 'settings.server.alertThresholds', description: 'web.alerts.lede', rows: [
      setting('alerts', 'storageWarningPercent', 'web.alerts.storageWarning', {type: 'number', min: 1, max: 50, unit: 'settings.unit.percent'}, {help: 'web.alerts.storageWarningHelp'}),
      setting('alerts', 'storageCriticalPercent', 'web.alerts.storageCritical', {type: 'number', min: 1, max: 50, unit: 'settings.unit.percent'}, {help: 'web.alerts.storageCriticalHelp'}),
      setting('alerts', 'certificateWarningDays', 'web.alerts.certificateWarning', {type: 'number', min: 1, max: 60, unit: 'settings.unit.days'}, {help: 'web.alerts.certificateWarningHelp'}),
    ]},
  ]},
];

// ── The structure ────────────────────────────────────────────────────────────

function allowed(item: Only, context: SettingsContext): boolean {
  return (!item.platforms || item.platforms.includes(context.platform)) && (!item.needs || item.needs(context.capabilities));
}

function strip<T extends Only>(item: T): Omit<T, 'platforms' | 'needs'> {
  const {platforms: _platforms, needs: _needs, ...rest} = item;
  return rest;
}

function resolve(pages: readonly AuthoredPage[], context: SettingsContext, administratorOnly = false): SettingsPage[] {
  const keys = context.capabilities.preferenceKeys;
  const out: SettingsPage[] = [];
  for (const authored of pages) {
    if (!allowed(authored, context)) continue;
    const sections: SettingsSection[] = [];
    for (const section of authored.sections) {
      if (!allowed(section, context) || administratorOnly && !section.administrators) continue;
      const rows = section.rows.filter(row => allowed(row, context) && (row.kind !== 'preference' || !keys || keys.has(row.key))).map(row => strip(row) as SettingsRow);
      if (rows.length) sections.push({id: section.id, ...(section.title ? {title: section.title} : {}), ...(section.description ? {description: section.description} : {}), ...(section.advanced ? {advanced: true} : {}), ...(section.tab ? {tab: section.tab} : {}), rows});
    }
    const tabs = authored.tabs?.filter(tab => sections.some(section => section.tab === tab.id));
    if (sections.length) out.push({id: authored.id, title: authored.title, icon: authored.icon, ...(tabs && tabs.length > 1 ? {tabs} : {}), sections});
  }
  return out;
}

/**
 * The words a row carries for whose its value is, or undefined when it says nothing. A value the
 * server keeps per kind of device names the kind plainly, so nobody takes it for a setting of the
 * one device in their hand.
 */
export function settingsScopeLabel(scope: SettingsScope | undefined, platform: SettingsPlatform): MessageId | undefined {
  if (scope === 'device') return 'settings.thisDevice';
  if (scope === 'deviceClass') return platform === 'web' ? 'settings.scope.allBrowsers' : platform === 'tv' ? 'settings.scope.allTVs' : 'settings.scope.allPhones';
  return undefined;
}

/** The Settings screen for this viewer on this device: Account for everyone, Server for owners and (what they may do) administrators. */
export function settingsStructure(context: SettingsContext): SettingsStructure {
  const headings: SettingsHeading[] = [{id: 'account', title: 'settings.heading.account', pages: resolve(accountPages, context)}];
  if (context.owner || context.administrator) {
    const pages = resolve(context.platform === 'tv' ? serverPages.filter(p => TV_SERVER_PAGES.has(p.id)) : serverPages, context, !context.owner);
    if (pages.length) headings.push({id: 'server', title: 'settings.heading.server', pages});
  }
  return Object.freeze({headings});
}

/** The page an address names (`/settings/<id>`), or the first page. Server pages are addressed as `server-<id>`. */
export function settingsPageId(heading: SettingsHeadingId, pageId: string): string {
  return heading === 'server' ? `server-${pageId}` : pageId;
}

/** Pages that moved keep their old account address resolving to the page that now holds them.
 * Only this explicit map - never a bare section-id lookup, which could collide with a server page. */
const movedAccountPages: Readonly<Record<string, string>> = {about: 'device'};

export function findSettingsPage(structure: SettingsStructure, address: string | undefined): Readonly<{heading: SettingsHeading; page: SettingsPage}> | undefined {
  const wanted = address === undefined ? undefined : movedAccountPages[address] ?? address;
  for (const heading of structure.headings) for (const p of heading.pages) if (settingsPageId(heading.id, p.id) === wanted) return {heading, page: p};
  const heading = structure.headings[0];
  return heading?.pages[0] ? {heading, page: heading.pages[0]} : undefined;
}

/** A row's stable identity within its page (for keys, anchors and search). */
export function settingsRowId(row: SettingsRow): string {
  return row.kind === 'preference' ? row.key : row.kind === 'server-setting' ? `${row.form}.${row.key}` : row.id;
}

/** The forms a page's rows are bound to: what its one Save covers besides the panels' own forms. */
export function serverFormsOf(page: SettingsPage): readonly ServerFormId[] {
  const out = new Set<ServerFormId>();
  for (const section of page.sections) for (const row of section.rows) if (row.kind === 'server-setting') out.add(row.form);
  return [...out];
}

/** The choices of a `choiceFrom` control, from what the server sent with the form. */
export function serverChoices(source: 'interfaces' | 'conversionModes', status: unknown, t: (id: MessageId, values?: MessageValues) => string, current?: string): readonly Readonly<{value: string; label: string}>[] {
  if (source === 'interfaces') {
    const interfaces = ((status as {interfaces?: readonly {name: string; up: boolean; loopback: boolean; virtual: boolean; addresses: readonly string[]}[]} | undefined)?.interfaces ?? []).filter(i => i.up && !i.loopback && i.addresses.length);
    return [
      {value: '', label: t('web.connectivity.interfaceAuto')},
      ...interfaces.map(i => ({value: i.name, label: t(i.virtual ? 'web.connectivity.interfaceVirtual' : 'web.connectivity.interfaceOption', {name: i.name, address: i.addresses[0]!.split('/')[0]!})})),
      // An interface that has gone away stays selectable as what it was, so saving another row does not silently change it.
      ...(current && !interfaces.some(i => i.name === current) ? [{value: current, label: current}] : []),
    ];
  }
  const modes = (status as {enumerations?: Readonly<Record<string, readonly string[]>>} | undefined)?.enumerations?.conversionMode ?? ['none'];
  return modes.map(value => ({value, label: t(value === 'none' ? 'web.dvrSettings.leaveRecorded' : value === 'remux' ? 'web.dvrSettings.repackage' : 'web.dvrSettings.convertSmaller')}));
}

/** Whether a server-setting row is shown for this draft: its value was sent by the server and its condition holds. */
export function serverRowShown(row: ServerSettingRow, value: (key: string) => unknown): boolean {
  if (value(row.key) === undefined) return false;
  if (!row.when) return true;
  const other = value(row.when.key);
  return 'equals' in row.when ? other === row.when.equals : other !== row.when.not;
}

/**
 * Whether a server-setting row is editable against this draft. Absent `enableWhen`, always;
 * otherwise while its key holds one of the listed values. This never hides the row.
 */
export function serverRowEnabled(row: ServerSettingRow, value: (key: string) => unknown): boolean {
  if (!row.enableWhen) return true;
  return row.enableWhen.in.includes(value(row.enableWhen.key));
}

export type SettingsMatch = Readonly<{address: string; pageTitle: string; rowId: string; label: string}>;

/** Rows whose words contain the query, across every page: the search field above the page list. */
export function searchSettings(structure: SettingsStructure, query: string, t: (id: MessageId, values?: MessageValues) => string, limit = 12): readonly SettingsMatch[] {
  const needle = query.trim().toLowerCase();
  if (needle.length < 2) return [];
  const out: SettingsMatch[] = [];
  for (const heading of structure.headings) {
    for (const p of heading.pages) {
      const pageTitle = t(p.title);
      for (const section of p.sections) {
        for (const row of section.rows) {
          if (!('label' in row) || !row.label) continue;
          const label = t(row.label);
          const help = 'help' in row && row.help ? t(row.help) : '';
          const choices = row.kind !== 'custom' && row.kind !== 'action' && row.control.type === 'choice' ? row.control.choices.map(c => t(c.label, c.labelValues)).join(' ') : '';
          if (`${label} ${help} ${choices} ${section.title ? t(section.title) : ''}`.toLowerCase().includes(needle)) out.push({address: settingsPageId(heading.id, p.id), pageTitle, rowId: settingsRowId(row), label});
          if (out.length >= limit) return out;
        }
      }
      if (pageTitle.toLowerCase().includes(needle) && !out.some(m => m.address === settingsPageId(heading.id, p.id))) out.push({address: settingsPageId(heading.id, p.id), pageTitle, rowId: '', label: pageTitle});
      if (out.length >= limit) return out;
    }
  }
  return out;
}

// ── Composite rows: one decision, several keys ───────────────────────────────

type Read = (key: string) => PreferenceValue | undefined;
const CARD_SIZE: Readonly<Record<string, number>> = {small: 85, medium: 100, large: 125};
const METHODS: Readonly<Record<string, Readonly<Record<string, string>>>> = {
  automatic: {'delivery.directPlay': 'prefer', 'delivery.directStream': 'allow', 'delivery.transcode': 'allow'},
  original: {'delivery.directPlay': 'require', 'delivery.directStream': 'never', 'delivery.transcode': 'never'},
  convert: {'delivery.directPlay': 'never', 'delivery.directStream': 'never', 'delivery.transcode': 'require'},
};

/** Composites whose value lives in the registry (the rest are the device's own). */
export function registryComposite(id: CompositeId): boolean {
  return id === 'playbackMethod' || id === 'cardSize' || id === 'playback.stillWatching';
}

/**
 * The choice a registry composite shows for the stored keys. A combination only an API client
 * could have written reads as the nearest choice a person can make, never as a fourth option.
 */
export function readComposite(id: CompositeId, read: Read): string | boolean {
  switch (id) {
    case 'playback.stillWatching': {
      const enabled = read('playback.passoutProtection'), episodes = read('playback.passoutAfterEpisodes');
      if (enabled === false) return '0';
      return enabled === true && typeof episodes === 'number' && Number.isInteger(episodes) && episodes >= 2 && episodes <= 5 ? String(episodes) : '';
    }
    case 'playbackMethod': {
      if (read('delivery.directPlay') === 'never') return 'convert';
      if (read('delivery.transcode') === 'never') return 'original';
      return 'automatic';
    }
    case 'cardSize': {
      const percent = Number(read('appearance.cardSizePercent') ?? 100);
      return percent < 90 ? 'small' : percent > 115 ? 'large' : 'medium';
    }
    default: return '';
  }
}

/** The keys a choice writes: one consistent combination, never a contradictory one. */
export function compositePatch(id: CompositeId, choice: string | boolean): PreferencePatch {
  switch (id) {
    case 'playback.stillWatching': {
      if (choice === '0') return {'playback.passoutProtection': false};
      if (typeof choice !== 'string' || !['2', '3', '4', '5'].includes(choice)) throw new Error('Invalid episode count.');
      return {'playback.passoutProtection': true, 'playback.passoutAfterEpisodes': Number(choice)};
    }
    case 'playbackMethod': return {...(METHODS[String(choice)] ?? METHODS.automatic!)};
    case 'cardSize': return {'appearance.cardSizePercent': CARD_SIZE[String(choice)] ?? 100};
    default: return {};
  }
}
