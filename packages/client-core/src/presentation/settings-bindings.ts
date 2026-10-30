import {clonePortableDto} from '../clone-portable-dto.ts';
import type {LibraryAdminDocument,LibrarySettings} from '../administration.ts';
import type {LibrarySonicDocument} from '../server-admin/library-sonic.ts';
import type {PutAdminLibrariesIdSonicAnalysisRequest} from '../../../contracts/src/types.ts';
import {setLibraryAnalysis,setLibraryScanDepth,libraryScanDepth,setLibrarySeekPreviews,setLibraryPreviewHours} from '../server-admin/library-policy.ts';
import type {AccessMember,MemberLimits} from '../server-administration.ts';
import type {SettingsBulkAccess} from '../settings-people.ts';
import type {InventorySource,SourceSettings} from '../library-inventory.ts';
import type {SettingsConfiguration, ConfigurationValue} from '../settings-configuration.ts';
import {canonicalQualityLadder,canonicalMusicQualityLadder} from '../../../contracts/src/types.ts';
import {validatePreferencePatch,type PreferenceSnapshot,type PreferenceScope,type PreferenceValue} from '../preferences.ts';
import type {ReadingPreferences} from '../preference-effects.ts';

export type SettingsBindingForm='runtime'|'connectivity'|'connectivityStatus'|'maintenance'|'alerts'|'preferences'|'device'|'remote'|'managedRoute'|'logs'|'library'|'metadata';
export type SettingsBinding=Readonly<{form:SettingsBindingForm;key:string;scope?:PreferenceScope;readOnly?:boolean;scale?:number;codec?:'insecure-connections'|'history-retention'|'number-choice'|'unlimited'|'domain'|'interface'|'remote-policy'|'away-quality'|'update-checks'|'window-mode'|'days'|'log-retention'|'still-watching'|'missing-rule'|'music-quality-choice'|'video-quality-choice'|'inherited-watched-threshold'|'surround'|'player-engine'|'tv-support'|'boolean'|'artwork-size'|'notice-audience'|'hardware-selection';fields?:Readonly<Record<string,string>>;window?:'heavy'|'backup'}>;
export type SettingsBindingSources=Readonly<{configuration?:SettingsConfiguration;preferences?:PreferenceSnapshot;forms?:Readonly<Partial<Record<SettingsBindingForm,Readonly<Record<string,unknown>>>>>;device?:Readonly<Record<string,unknown>>;deviceDefaults?:Readonly<Record<string,unknown>>}>;
export type SettingsBindingValue=Readonly<{current:unknown;effective:unknown;default:unknown;source:string;configOnly:boolean;applicationMode?:string}>;
export type SettingsBindingPatch=Readonly<{form:SettingsBindingForm;values:Readonly<Record<string,unknown>>}>;
const bindings:Readonly<Record<string,SettingsBinding>>={
 'remote.identityCode':{form:'connectivityStatus',key:'identityCode',readOnly:true},
 'metadata.artworkSize':{form:'metadata',key:'artworkMaxEdge',codec:'artwork-size'},
 'device.player':{form:'device',key:'playerEngine',codec:'player-engine'},
 'device.receive':{form:'device',key:'receiveFromDevices',codec:'boolean'},
 'device.lossNotices':{form:'device',key:'lossNotices',codec:'boolean'},
 // Plex's "Allow insecure connections" (P2-69): never, on the home network (default) or always.
 'device.allowInsecure':{form:'device',key:'allowInsecure',codec:'insecure-connections'},
 'audioSubtitles.surround':{form:'device',key:'forceStereo',codec:'surround'},
 'quality.disableHdr':{form:'device',key:'disableHdr',codec:'boolean'},
 'quality.tvSupports':{form:'device',key:'tvSupports',fields:{hdr:'tvHdr',surround:'tvSurround'},codec:'tv-support'},
 'playback.skipping':{form:'preferences',key:'playback.skipping',scope:'profile-server',fields:{intro:'playback.introSkip',credits:'playback.creditsSkip',recap:'playback.recapSkip'}},
 'playback.stillWatching':{form:'preferences',key:'playback.passoutProtection',scope:'profile-server',fields:{enabled:'playback.passoutProtection',episodes:'playback.passoutAfterEpisodes'},codec:'still-watching'},
 'storage.missingRule':{form:'runtime',key:'library.missingTitles',codec:'missing-rule'},
 'storage.missingDaysExact':{form:'runtime',key:'library.missingTitlesDays'},
 'segments.libraryModes':{form:'runtime',key:'segments.libraryModes',fields:{tv:'segments.tvMode',anime:'segments.animeMode',movies:'segments.movieMode'}},
 'segments.shortestEpisode':{form:'runtime',key:'segments.shortestEpisode'},
 'segments.shortestFilm':{form:'runtime',key:'segments.shortestFilm'},
 'segments.introWindow':{form:'runtime',key:'segments.introWindow'},
 'segments.creditsWindow':{form:'runtime',key:'segments.creditsWindow'},
 'segments.filmCreditsWindow':{form:'runtime',key:'segments.filmCreditsWindow'},
 'segments.chapters':{form:'runtime',key:'segments.useChapterNames',codec:'boolean'},
 'segments.viewerFloor':{form:'runtime',key:'segments.viewerConfidencePercent',codec:'number-choice'},
 'segments.autoFloor':{form:'runtime',key:'segments.automaticConfidencePercent',codec:'number-choice'},
 'segments.mode':{form:'runtime',key:'library.segmentDetection'},
 'people.inviteExpiry':{form:'runtime',key:'people.invitation_days',codec:'number-choice'},
 'people.inviteRole':{form:'runtime',key:'people.invitation_role'},
 'people.maxStreamsEach':{form:'runtime',key:'perAccountCap',codec:'unlimited'},
 'people.communityActivity':{form:'runtime',key:'home.communityActivityEnabled'},
 'integrations.compatibility':{form:'runtime',key:'integrations.compatibilityEnabled',codec:'boolean'},
 'integrations.requestsPerMinute':{form:'runtime',key:'apiRequestsPerMinute'},
 'streaming.whenCantPlay':{form:'runtime',key:'planningPolicy'},
 'general.name':{form:'runtime',key:'name'},
 'general.updateChecks':{form:'runtime',key:'updates.feedUrl',codec:'update-checks'},
 'general.updateFeed':{form:'runtime',key:'updates.feedUrl'},
 'updates.channel':{form:'runtime',key:'updates.releaseChannel'},
 'streaming.allowConversion':{form:'runtime',key:'conversionMode'},
 // The drawn Conversion location is one stored directory: "separate" writes no
 // path, "memory" writes the platform's RAM file system, and the child folder
 // row writes the chosen one. The three-way choice is projected by the page
 // from the capacity facts, never stored as a second setting.
 'streaming.transcodeFolder':{form:'runtime',key:'temporaryDirectory'},
 'streaming.outputFormat':{form:'runtime',key:'outputFormat'},
 'streaming.musicAwayQuality':{form:'runtime',key:'musicAwayQuality',codec:'music-quality-choice'},
 'streaming.downloadQuality':{form:'runtime',key:'downloadMaxQuality',codec:'video-quality-choice'},
 'streaming.uploadSpeed':{form:'connectivity',key:'uploadCapacityKbps',scale:1000},
 'streaming.awayQuality':{form:'connectivity',key:'remoteBitrateLimitKbps',codec:'away-quality'},
 'streaming.awayExact':{form:'connectivity',key:'remoteBitrateLimitKbps',scale:1000},
 // The drawn Device selector needs the checked opaque device-ID producer.
 // hardwareBackend names a method and cannot represent this decision; the
 // former free-text streaming.hardwareDevice row is retired by 812.
 'streaming.toneMapping':{form:'runtime',key:'hdrToneMapping'},
 'streaming.toneMappingMethod':{form:'runtime',key:'hdrToneMappingAlgorithm'},
 'streaming.encoderSpeed':{form:'runtime',key:'x264Preset'},
 'streaming.repackage':{form:'runtime',key:'directStreamRemux'},
 'streaming.ffmpegPath':{form:'runtime',key:'ffmpegPath'},
 'streaming.hardware':{form:'runtime',key:'hardwareSelection',codec:'hardware-selection'},
 'streaming.hardwareDecode':{form:'runtime',key:'hardwareDecode',codec:'boolean'},
 'streaming.convertAhead':{form:'runtime',key:'throttleBufferMs',scale:1000,codec:'number-choice'},
 'streaming.rewindKeep':{form:'runtime',key:'playedRetentionMs',scale:1000,codec:'number-choice'},
 'streaming.sessionSpace':{form:'runtime',key:'sessionStorageMiB',codec:'number-choice'},
 'streaming.transcodeLocation':{form:'runtime',key:'transcodeLocation'},
 'streaming.memoryCap':{form:'runtime',key:'memoryCap'},
 'streaming.maxConversions':{form:'runtime',key:'maxConcurrentSessions',codec:'unlimited'},
 'streaming.maxHardwareConversions':{form:'runtime',key:'maxHardwareSessions',codec:'unlimited'},
 'streaming.maxSoftwareConversions':{form:'runtime',key:'maxSoftwareSessions',codec:'unlimited'},
 'streaming.maxBackgroundConversions':{form:'runtime',key:'maxBackgroundSessions',codec:'unlimited'},
 'streaming.maxStreams':{form:'runtime',key:'serverCap',codec:'unlimited'},
 'streaming.pausedTimeout':{form:'connectivity',key:'pausedSessionTimeoutMinutes',codec:'number-choice'},
 'remote.ownCertificate':{form:'connectivity',key:'ownAddressEnabled'},
 'remote.externalAddress':{form:'connectivity',key:'customCertificateDomain',codec:'domain'},
 'remote.certFile':{form:'connectivity',key:'customCertificatePath'},
 'remote.certKey':{form:'connectivity',key:'customCertificateKeyPath'},
 'remote.homeNets':{form:'connectivity',key:'lanNetworks'},
 // Plex's "Secure connections": Required refuses plain HTTP for everyone, at home too (P2-67/P2-69).
 'remote.secureConnections':{form:'connectivity',key:'secureConnections'},
 'remote.discovery':{form:'connectivity',key:'lanDiscoveryEnabled'},
 'remote.dlna':{form:'connectivity',key:'dlnaEnabled'},
 'remote.interface':{form:'connectivity',key:'advertisedInterface',codec:'interface'},
 'remote.connectionLimit':{form:'connectivity',key:'connectionLimit',codec:'unlimited'},
 'remote.extraAddresses':{form:'connectivity',key:'accessUrls'},
 'remote.trustedProxies':{form:'connectivity',key:'trustedProxies'},
 'remote.who':{form:'connectivity',key:'remoteSignInPolicy',codec:'remote-policy'},
 'general.heavyWork':{form:'maintenance',key:'enabled',window:'heavy',codec:'window-mode'},
 'general.heavyStart':{form:'maintenance',key:'startMinute',window:'heavy',codec:'number-choice'},
 'general.heavyLength':{form:'maintenance',key:'durationMinutes',window:'heavy',scale:60,codec:'number-choice'},
 'general.heavyDays':{form:'maintenance',key:'days',window:'heavy',codec:'days'},
 'storage.backupSchedule':{form:'maintenance',key:'enabled',window:'backup',codec:'window-mode'},
 'storage.backupTime':{form:'maintenance',key:'startMinute',window:'backup',codec:'number-choice'},
 'storage.backupWindowLength':{form:'maintenance',key:'durationMinutes',window:'backup',scale:60,codec:'number-choice'},
 'storage.backupDays':{form:'maintenance',key:'days',window:'backup',codec:'days'},
 'history.keepHistory':{form:'runtime',key:'playHistoryDays',codec:'history-retention'},
 'general.backgroundSpeed':{form:'maintenance',key:'backgroundSpeed'},
 'general.alertStorageWarn':{form:'alerts',key:'storageWarningPercent',codec:'number-choice'},
 'general.alertStorageCritical':{form:'alerts',key:'storageCriticalPercent',codec:'number-choice'},
 'general.alertCertificateDays':{form:'alerts',key:'certificateWarningDays',codec:'number-choice'},
 'general.noticeAudience':{form:'alerts',key:'noticeAudience',codec:'notice-audience'},
 'storage.analysisSpace':{form:'runtime',key:'analysisStorageGiB',codec:'number-choice'},
 'storage.analysisStage':{form:'runtime',key:'analysisStageStorageMiB',codec:'number-choice'},
 'storage.subtitleTracks':{form:'runtime',key:'subtitleTrackLimit',codec:'number-choice'},
 'storage.subtitleFile':{form:'runtime',key:'subtitleFileLimitMiB',codec:'number-choice'},
 'storage.keepNotifications':{form:'runtime',key:'notificationDays',codec:'number-choice'},
 'storage.backupKeep':{form:'maintenance',key:'backupKeepCount',codec:'number-choice'},
 'storage.backupKeepExact':{form:'maintenance',key:'backupKeepCount'},
 'storage.keepConversions':{form:'maintenance',key:'retention.conversions',codec:'number-choice'},
 'storage.keepPrepared':{form:'maintenance',key:'retention.prepared',codec:'number-choice'},
 'storage.keepDownloads':{form:'maintenance',key:'retention.downloads',codec:'number-choice'},
 'storage.keepSubtitles':{form:'maintenance',key:'retention.subtitles',codec:'number-choice'},
 'storage.keepTrickplay':{form:'maintenance',key:'retention.trickplay',codec:'number-choice'},
 'troubleshooting.detailLength':{form:'logs',key:'logDetailMinutes',codec:'number-choice'},
 'troubleshooting.logLevel':{form:'logs',key:'logLevel'},
 'troubleshooting.keepLogs':{form:'runtime',key:'diagnosticDays',codec:'number-choice'},
 'troubleshooting.keepLogsByKind':{form:'runtime',key:'logRetention',codec:'log-retention'},
};
const preferenceKeys:Readonly<Record<string,string>>={
 'audioSubtitles.preferredAudio':'playback.preferredAudioLanguages','audioSubtitles.preferredSubtitles':'playback.preferredSubtitleLanguages','audioSubtitles.subtitleMode':'playback.subtitleMode','audioSubtitles.subtitleSize':'playback.subtitleSize','audioSubtitles.subtitleBackground':'playback.subtitleBackground',
 'quality.home':'quality.home','quality.remote':'quality.remote','quality.mobileData':'quality.mobile',
 'quality.directPlay':'delivery.directPlay','quality.directStream':'delivery.directStream','quality.transcode':'delivery.transcode',
 'privacy.showActivity':'privacy.showActivityToMembers','privacy.includeWatchTogether':'privacy.includeInWatchTogether','privacy.pauseHistory':'privacy.pauseWatchHistory','privacy.rememberSearches':'search.rememberHistory',
 'music.audioNormalization':'music.audioNormalization','music.crossfadeMs':'music.crossfadeMs','music.gapless':'music.gapless','playback.showSyncedLyrics':'playback.showSyncedLyrics','playback.audiobookSpeed':'audiobooks.defaultSpeed','music.idlePause':'playback.sleepTimerMinutes',
 'music.qualityAway':'music.qualityAway','music.qualityMobile':'music.qualityMobile',
 'language.locale':'region.locale','language.hourCycle':'region.hourCycle',
 'playback.autoplayNext':'playback.autoplayNext','playback.upNextCountdownMs':'playback.upNextCountdownMs','playback.startNextDuringCredits':'playback.startNextDuringCredits','playback.skipBackMs':'playback.skipBackMs','playback.skipForwardMs':'playback.skipForwardMs','playback.playedThresholdPercent':'playback.playedThresholdPercent','playback.startedThresholdPercent':'playback.startedThresholdPercent',
 'appearance.backdrops':'appearance.showBackdrops','appearance.cardSizeExact':'appearance.cardSizePercent','appearance.notificationDot':'notifications.badges',
};
const libraryScalarBindings:Readonly<Record<string,SettingsBinding>>={
 'libraries.collections':{form:'library',key:'groupCollections'},
 'libraries.allowDelete':{form:'library',key:'allowMediaDeletion'},
 'libraries.localTags':{form:'library',key:'localTags'},
 'libraries.lyrics':{form:'library',key:'lyricsSource'},
 'libraries.watch':{form:'library',key:'monitorChanges'},
 'libraries.scrubSpacing':{form:'library',key:'navigation.trickplayIntervalMs',scale:1000},
 'libraries.scrubWidth':{form:'library',key:'navigation.trickplayTileWidth'},
 'libraries.titleSource':{form:'library',key:'titleSource'},
 'libraries.episodeNumbering':{form:'library',key:'episodeNumbering'},
 'libraries.extras':{form:'library',key:'findExtras'},
 'libraries.sortArticles':{form:'library',key:'sortIgnoreArticles'},
 'libraries.refreshMode':{form:'library',key:'refreshMode'},
 'libraries.acoustid':{form:'library',key:'acoustidEnabled'},
 'libraries.cwWeeks':{form:'library',key:'continueWatching.weeks'},
 'libraries.cwMax':{form:'library',key:'continueWatching.maximumItems'},
 'libraries.cwPremieres':{form:'library',key:'continueWatching.includeSeasonPremieres'},
 'libraries.cwWatched':{form:'library',key:'continueWatching.videoPlayedThreshold',codec:'number-choice'},
 'libraries.cwRule':{form:'library',key:'continueWatching.videoCompletion'},
};
const source69PreferenceBindings:Readonly<Record<string,SettingsBinding>>={
 'appearance.backdrops':{form:'preferences',key:'appearance.showBackdrops',scope:'profile-device-class'},
 'appearance.textSize':{form:'preferences',key:'appearance.textSize',scope:'profile-device'},
 'appearance.motion':{form:'preferences',key:'appearance.motion',scope:'profile-device'},
 'appearance.cardSizeExact':{form:'preferences',key:'appearance.cardSizePercent',scope:'profile-device-class'},
 'appearance.libraryView':{form:'preferences',key:'appearance.libraryView',scope:'profile-device'},
 'appearance.episodesLayout':{form:'preferences',key:'appearance.episodesLayout',scope:'profile-device'},
 'appearance.layout':{form:'preferences',key:'appearance.layout',scope:'profile-device'},
 'audioSubtitles.preferredAudio':{form:'preferences',key:'playback.preferredAudioLanguages',scope:'profile-server'},
 'audioSubtitles.preferredSubtitles':{form:'preferences',key:'playback.preferredSubtitleLanguages',scope:'profile-server'},
 'audioSubtitles.subtitleMode':{form:'preferences',key:'playback.subtitleMode',scope:'profile-server'},
 'audioSubtitles.subtitleSize':{form:'preferences',key:'playback.subtitleSize',scope:'profile-server'},
 'audioSubtitles.subtitleColor':{form:'preferences',key:'playback.subtitleColor',scope:'profile-server'},
 'audioSubtitles.subtitleBackground':{form:'preferences',key:'playback.subtitleBackground',scope:'profile-server'},
 'audioSubtitles.subtitlePosition':{form:'preferences',key:'playback.subtitlePosition',scope:'profile-server'},
 'audioSubtitles.audioDescription':{form:'preferences',key:'playback.audioDescription',scope:'profile-server'},
 'audioSubtitles.trackChoice':{form:'preferences',key:'playback.audioTrackChoice',scope:'profile-server'},
 'audioSubtitles.systemCaptions':{form:'preferences',key:'playback.useSystemCaptionStyle',scope:'profile-device'},
 'audioSubtitles.preferSdh':{form:'preferences',key:'playback.preferSdhSubtitles',scope:'profile-server'},
 'audioSubtitles.forcedFallback':{form:'preferences',key:'playback.forcedSubtitleFallback',scope:'profile-server'},
 'audioSubtitles.untaggedForced':{form:'preferences',key:'playback.useUntaggedForced',scope:'profile-server'},
 'audioSubtitles.subtitleFont':{form:'preferences',key:'playback.subtitleFont',scope:'profile-server'},
 'audioSubtitles.styledSubtitles':{form:'preferences',key:'playback.keepSubtitleStyling',scope:'profile-server'},
 'language.locale':{form:'preferences',key:'region.locale',scope:'profile-server'},
 'language.hourCycle':{form:'preferences',key:'region.hourCycle',scope:'profile-server'},
 'language.formats':{form:'preferences',key:'region.formats',scope:'profile-server'},
 'language.dateOrder':{form:'preferences',key:'region.dateOrder',scope:'profile-server'},
 'language.firstDay':{form:'preferences',key:'region.firstDay',scope:'profile-server'},
 'language.sizeUnits':{form:'preferences',key:'region.sizeUnits',scope:'profile-server'},
 'playback.showSyncedLyrics':{form:'preferences',key:'playback.showSyncedLyrics',scope:'profile-server'},
 'playback.audiobookSpeed':{form:'preferences',key:'audiobooks.defaultSpeed',scope:'profile-server'},
 'playback.autoplayNext':{form:'preferences',key:'playback.autoplayNext',scope:'profile-server'},
 'playback.upNextCountdownMs':{form:'preferences',key:'playback.upNextCountdownMs',scope:'profile-server'},
 'playback.startNextDuringCredits':{form:'preferences',key:'playback.startNextDuringCredits',scope:'profile-server'},
 'playback.skipBackMs':{form:'preferences',key:'playback.skipBackMs',scope:'profile-server'},
 'playback.skipForwardMs':{form:'preferences',key:'playback.skipForwardMs',scope:'profile-server'},
 'playback.playedThresholdPercent':{form:'preferences',key:'playback.playedThresholdPercent',scope:'profile-server',codec:'inherited-watched-threshold'},
 'playback.startedThresholdPercent':{form:'preferences',key:'playback.startedThresholdPercent',scope:'profile-server'},
 'privacy.showActivity':{form:'preferences',key:'privacy.showActivityToMembers',scope:'profile-server'},
 'privacy.includeWatchTogether':{form:'preferences',key:'privacy.includeInWatchTogether',scope:'profile-server'},
 'privacy.pauseHistory':{form:'preferences',key:'privacy.pauseWatchHistory',scope:'profile-server'},
 'privacy.rememberSearches':{form:'preferences',key:'search.rememberHistory',scope:'profile-server'},
 'appearance.notificationDot':{form:'preferences',key:'notifications.badges',scope:'profile-server'},
 'quality.home':{form:'preferences',key:'quality.home',scope:'profile-device-class',codec:'video-quality-choice'},
 'quality.remote':{form:'preferences',key:'quality.remote',scope:'profile-device-class',codec:'video-quality-choice'},
 'quality.directPlay':{form:'preferences',key:'delivery.directPlay',scope:'profile-device-class'},
 'quality.directStream':{form:'preferences',key:'delivery.directStream',scope:'profile-device-class'},
 'quality.transcode':{form:'preferences',key:'delivery.transcode',scope:'profile-device-class'},
};
// Runtime keys and scopes come from the published Music registry. CD is only
// the drawn choice value; the canonical stored quality remains cd-quality.
const musicPreferenceBindings:Readonly<Record<string,SettingsBinding>>={
 'music.audioNormalization':{form:'preferences',key:'music.audioNormalization',scope:'profile-server'},
 'music.crossfadeMs':{form:'preferences',key:'music.crossfadeMs',scope:'profile-server'},
 'music.gapless':{form:'preferences',key:'music.gapless',scope:'profile-server'},
 'music.neverBoost':{form:'preferences',key:'music.neverBoost',scope:'profile-server'},
 'music.smoothTransitions':{form:'preferences',key:'music.smoothTransitions',scope:'profile-server'},
 'music.dsdGainDb':{form:'preferences',key:'music.dsdGainDb',scope:'profile-server'},
 'music.qualityHome':{form:'preferences',key:'music.qualityHome',scope:'profile-device-class',codec:'music-quality-choice'},
 'music.qualityAway':{form:'preferences',key:'music.qualityAway',scope:'profile-device-class',codec:'music-quality-choice'},
 'music.qualityMobile':{form:'preferences',key:'music.qualityMobile',scope:'profile-device-class',codec:'music-quality-choice'},
 'music.downloadQuality':{form:'preferences',key:'music.qualityDownloads',scope:'profile-device-class',codec:'music-quality-choice'},
 // The generated Music page's Audiobooks group. Their row ids are the profile
 // preference keys themselves, so the generic row-id map had no entry for them.
 'audiobooks.skipBackMs':{form:'preferences',key:'audiobooks.skipBackMs',scope:'profile-server'},
 'audiobooks.skipForwardMs':{form:'preferences',key:'audiobooks.skipForwardMs',scope:'profile-server'},
 'audiobooks.resumeRewindMs':{form:'preferences',key:'audiobooks.resumeRewindMs',scope:'profile-server'},
};
export function settingsBinding(rowId:string):SettingsBinding|undefined{
 for(const table of [bindings,libraryScalarBindings,source69PreferenceBindings,musicPreferenceBindings])if(Object.hasOwn(table,rowId))return table[rowId];
 return Object.hasOwn(preferenceKeys,rowId)?{form:'preferences',key:preferenceKeys[rowId]!}:undefined;
}

/** Settings reads Reading through readingPreferences and submits changes through
 * its patch facade. This is the sole row-to-property map, with no reader effect
 * or extra preference key. The write facade still owns value validation/CAS. */
const readingProperties:Readonly<Record<string,keyof ReadingPreferences>>=Object.freeze({
 'reading.theme':'theme','reading.font':'font','reading.textSize':'textSize',
 'reading.lineSpacing':'lineSpacing','reading.margins':'margins','reading.alignment':'alignment',
 'reading.layout':'layout','reading.twoPages':'twoPages','reading.tapToTurn':'tapToTurn',
 'reading.hyphenation':'hyphenation','reading.highlightColour':'highlightColour','reading.pdfView':'pdfView',
 'comics.fit':'comicFit','comics.direction':'comicDirection','comics.firstPageAlone':'firstPageAlone',
});
export function readingSettingsProperty(rowId:string):keyof ReadingPreferences|undefined{return Object.hasOwn(readingProperties,rowId)?readingProperties[rowId]:undefined;}
function at(source:Readonly<Record<string,unknown>>|undefined,key:string):unknown{if(source&&Object.hasOwn(source,key))return source[key];return key.split('.').reduce<unknown>((v,key)=>v&&typeof v==='object'?(v as Record<string,unknown>)[key]:undefined,source);}
const days=['monday','tuesday','wednesday','thursday','friday','saturday','sunday'] as const;
const categories=['server','playback','scan','network','client'] as const;
const cadences:Readonly<Record<string,readonly string[]>>={nightly:days,weekdays:days.slice(0,5),weekends:days.slice(5),weekly:['sunday']};
function defaultFeed(sources:SettingsBindingSources):unknown{return sources.configuration?.fields.find(f=>f.id==='general.updateFeed')?.default;}
function windows(sources:SettingsBindingSources):readonly Readonly<Record<string,unknown>>[]{const v=sources.forms?.maintenance?.windows;return Array.isArray(v)?v:[];}
function matchingWindow(w:Readonly<Record<string,unknown>>,role:'heavy'|'backup'):boolean { const tasks=w.tasks;return Array.isArray(tasks)&&(role==='backup'?tasks.includes('backup'):tasks.includes('analysis')||tasks.includes('trickplay')); }
function windowMode(binding:SettingsBinding,w:Readonly<Record<string,unknown>>):unknown{
 if(!w.enabled)return binding.window==='heavy'?'any':'never';
 if(binding.window==='heavy')return w.startMinute===60&&w.durationMinutes===300&&Array.isArray(w.days)&&w.days.length===7&&days.every(d=>(w.days as unknown[]).includes(d))&&!w.timezone?'overnight':'custom';
 return Object.entries(cadences).find(([,wanted])=>Array.isArray(w.days)&&w.days.length===wanted.length&&wanted.every(d=>(w.days as unknown[]).includes(d)))?.[0]??'custom';
}
const historyRetentionDays:Readonly<Record<string,number>>={forever:0,'3y':1095,'1y':365,'180d':180,'90d':90,'30d':30};
function presented(binding:SettingsBinding,v:unknown,sources:SettingsBindingSources):unknown{
 if(v===undefined)return undefined;
 if(binding.codec==='history-retention')return typeof v==='number'&&Number.isSafeInteger(v)&&v>=0&&v<=36500?Object.entries(historyRetentionDays).find(([,days])=>days===v)?.[0]??String(v):undefined;
 if(binding.codec==='music-quality-choice')return typeof v==='string'&&canonicalMusicQualityLadder.some(q=>q.id===v)?(v==='cd-quality'?'cd':v):undefined;
 if(binding.codec==='video-quality-choice')return typeof v==='string'&&canonicalQualityLadder.some(q=>q.id===v)&&(binding.key!=='downloadMaxQuality'||v!=='automatic')?(v==='2160p'?'4k':v):undefined;
 if(binding.codec==='artwork-size')return v===1280?'small':v===1920?'standard':v===2560?'large':undefined;
 if(binding.codec==='notice-audience')return validNoticeAudience(v)?v:undefined;
 if(binding.codec==='hardware-selection')return validHardwareSelection(v)?v:undefined;
 if(binding.codec==='boolean')return typeof v==='boolean'?v:undefined;
 if(binding.codec==='insecure-connections')return v==='never'||v==='local'||v==='always'?v:undefined;
 if(binding.codec==='surround')return typeof v==='boolean'?(v?'stereo':'asis'):undefined;
 if(binding.codec==='player-engine')return v==='automatic'?'auto':v==='native'||v==='mpv'?v:undefined;

 if(binding.codec==='missing-rule'){const n=sources.configuration?.fields.find(f=>f.id==='storage.missingDaysExact')?.current??sources.forms?.runtime?.['library.missingTitlesDays'];return v==='never'?'never':v==='immediately'?'now':v==='after_days'?(n===7?'7d':n===30?'30d':n===90?'90d':'custom'):undefined;}
 if(binding.key==='planningPolicy')return v==='minimize_server_work'?'least':v==='maximum_fidelity'?'most':undefined;
 if(binding.codec==='update-checks'){const feed=defaultFeed(sources);return v===''?'never':feed===undefined?undefined:v===feed?'auto':'custom';}
 if(binding.codec==='away-quality')return v===0?'original':canonicalQualityLadder.find(q=>q.height>0&&q.videoKbps===v)?.id.replace('2160p','4k')??'custom';
 if(binding.codec==='domain')return v?`https://${String(v)}`:'';
 if(binding.codec==='interface')return v||'all';
 if(binding.codec==='remote-policy')return v==='allow'?'anyone':v==='owner-only'?'owner':v==='off'?'none':undefined;
 if(binding.codec==='unlimited')return v===0||v===null?'unlimited':String(v);
 if(binding.codec==='days'&&Array.isArray(v)){const chosen=v;return Object.fromEntries(days.map(d=>[d,chosen.includes(d)?'runs':'skips']));}
 if(binding.codec==='log-retention'&&Array.isArray(v)){const entries=v as {category:string;days:number}[];return Object.fromEntries(categories.map(k=>[k,String(entries.find(r=>r.category===k)?.days??'default')]));}
 if(binding.scale){if(v===0&&binding.key==='uploadCapacityKbps')return null;v=typeof v==='number'?v/binding.scale:v;}
 return binding.codec==='number-choice'?String(v):v;
}
/** Settings › General › Who sees each notice: every notice kind, each sent to both, the owner, or no one. */
function validNoticeAudience(v:unknown):v is Readonly<Record<string,'both'|'owner'|'off'>>{
 if(!v||typeof v!=='object'||Array.isArray(v))return false;
 const entries=Object.entries(v),kinds=['scan','storage','certificate','recording','feedback'];
 return entries.length===kinds.length&&entries.every(([k,x])=>kinds.includes(k)&&(x==='both'||x==='owner'||x==='off'));
}
/** Settings › Streaming › Device: automatic, the processor, or one adapter's opaque id from the hardware report. */
function validHardwareSelection(v:unknown):v is string{return typeof v==='string'&&(v==='auto'||v==='software'||/^[a-z0-9_-]{1,80}$/.test(v));}
export function readSettingsBinding(binding:SettingsBinding,sources:SettingsBindingSources,rowId?:string):SettingsBindingValue|undefined{
 if(binding.fields){
  const values=Object.entries(binding.fields).map(([name,key])=>[name,readSettingsBinding({form:binding.form,key,scope:binding.scope},sources,binding.key==='segments.libraryModes'?key:undefined)] as const);
  if(values.some(([,value])=>!value))return undefined;
  const part=(kind:'current'|'effective'|'default')=>{
   const v=Object.fromEntries(values.map(([name,value])=>[name,value![kind]]));
   if(binding.codec==='still-watching')return typeof v.enabled==='boolean'&&typeof v.episodes==='number'&&Number.isSafeInteger(v.episodes)&&v.episodes>=2&&v.episodes<=5?(v.enabled?v.episodes:0):undefined;
   if(binding.codec==='tv-support')return typeof v.hdr==='boolean'&&typeof v.surround==='boolean'?(v.hdr?(v.surround?'hdr-surround':'hdr'):(v.surround?'surround':'none')):undefined;
   return v;
  };
  const current=part('current'),effective=part('effective');if(current===undefined||effective===undefined)return undefined;
  return {current,effective,default:part('default'),source:[...new Set(values.map(([,v])=>v!.source))].join(','),configOnly:false};
 }
 if(binding.form==='preferences'){
  const snapshot=sources.preferences,field=snapshot?.registry.fields.find(f=>f.key===binding.key);if(!snapshot||!field)return undefined;
  const scope=binding.scope??(field.scopes.length===1?field.scopes[0]:undefined);
  if(!scope||!field.scopes.includes(scope)||scope==='profile-device'&&!snapshot.deviceBound)return undefined;
  const document=snapshot.documents.find(d=>d.scope===scope),source=snapshot.effectiveSource[binding.key];
  if(!document||source!=='default'&&source!=='policy'&&source!==scope)return undefined;
  const stored=Object.hasOwn(document.values,binding.key),effective=snapshot.effective[binding.key];
  if(stored&&source==='default'||!stored&&source===scope||effective===undefined)return undefined;
  if(binding.codec==='inherited-watched-threshold'){
   if(field.type!=='integer'||!validPublishedPreference(snapshot,scope,binding.key,field.default)||!validPublishedPreference(snapshot,scope,binding.key,effective))return undefined;
   if(!stored&&source==='default')return {current:'server',effective:'server',default:'server',source,configOnly:false};
   const current=document.values[binding.key];
   if(!stored||source!==scope||!validPublishedPreference(snapshot,scope,binding.key,current)||!validPublishedPreference(snapshot,scope,binding.key,effective))return undefined;
   return {current,effective,default:'server',source,configOnly:false};
  }
  const raw=stored?document.values[binding.key]:effective;
  if(!validPublishedPreference(snapshot,scope,binding.key,raw)||!validPublishedPreference(snapshot,scope,binding.key,effective))return undefined;
  const current=presented(binding,raw,sources),shown=presented(binding,effective,sources),defaultValue=presented(binding,field.default,sources);
  if(current===undefined||shown===undefined||defaultValue===undefined)return undefined;
  return {current,effective:shown,default:defaultValue,source,configOnly:false};
 }
 const alias=rowId==='general.updateChecks'?'general.updateFeed':rowId==='streaming.awayQuality'?'streaming.awayExact':rowId==='storage.backupKeep'?'storage.backupKeepExact':rowId;
 const fact:ConfigurationValue|undefined=alias?sources.configuration?.fields.find(f=>f.id===alias):undefined;
 if(fact){const display={...binding,scale:undefined,...(rowId==='streaming.awayQuality'?{scale:1/1000}:{})};const show=(v:unknown)=>rowId==='streaming.awayQuality'?presented(binding,typeof v==='number'?v*1000:v,sources):presented(display,v,sources);const current=show(fact.current),effective=show(fact.effective);if(current===undefined||effective===undefined)return undefined;return {current,effective,default:show(fact.default),source:fact.source,configOnly:fact.configOnly,applicationMode:fact.applicationMode};}
 const w=binding.window?windows(sources).find(w=>matchingWindow(w,binding.window!)):undefined;
 const raw=binding.window?(binding.codec==='window-mode'?w&&windowMode(binding,w):w?.[binding.key]):at(binding.form==='device'?sources.device:sources.forms?.[binding.form],binding.key);if(raw===undefined)return undefined;
 const current=presented(binding,raw,sources);if(current===undefined)return undefined;return {current,effective:current,default:binding.form==='device'?presented(binding,at(sources.deviceDefaults,binding.key),sources):undefined,source:binding.form,configOnly:false};
}
function checkedPreferencePatch(snapshot:PreferenceSnapshot,scope:PreferenceScope,values:Readonly<Record<string,unknown>>):Readonly<Record<string,PreferenceValue|null>>{
 const patch=validatePreferencePatch(snapshot.registry,scope,values as Readonly<Record<string,PreferenceValue|null>>);
 for(const [key,value] of Object.entries(patch)){
  const field=snapshot.registry.fields.find(f=>f.key===key)!;
  if(typeof value==='number'&&(field.min!==undefined&&value<field.min||field.max!==undefined&&value>field.max))throw new Error('Invalid setting value.');
 }
 return patch;
}
function validPublishedPreference(snapshot:PreferenceSnapshot,scope:PreferenceScope,key:string,value:unknown):boolean{
 if(value===null||value===undefined)return false;
 try{checkedPreferencePatch(snapshot,scope,{[key]:value});return true;}catch{return false;}
}
function preferenceBindingPatch(binding:SettingsBinding,values:Readonly<Record<string,unknown>>,sources:SettingsBindingSources):SettingsBindingPatch{
 const snapshot=sources.preferences;if(!snapshot)throw new Error('Invalid setting value.');
 const fields=Object.keys(values).map(key=>snapshot.registry.fields.find(f=>f.key===key));
 const first=fields[0],scope=binding.scope??(first?.scopes.length===1?first.scopes[0]:undefined);
 if(!scope||scope==='profile-device'&&!snapshot.deviceBound||!snapshot.documents.some(d=>d.scope===scope)||fields.some(f=>!f||!f.scopes.includes(scope)))throw new Error('Invalid setting value.');
 return {form:'preferences',values:checkedPreferencePatch(snapshot,scope,values)};
}
/** A write always targets an existing producer; Advanced values are not rounded to a preset. */
export function settingsBindingPatch(binding:SettingsBinding,value:unknown,sources:SettingsBindingSources={}):SettingsBindingPatch{
 if(binding.readOnly||binding.form==='connectivityStatus')throw new Error('This setting is read-only.');
 if(binding.fields){
  if(binding.codec==='still-watching'){
   if(typeof value!=='number'||!Number.isSafeInteger(value)||![0,2,3,4,5].includes(value))throw new Error('Invalid episode count.');
   return preferenceBindingPatch(binding,value===0?{'playback.passoutProtection':false}:{'playback.passoutProtection':true,'playback.passoutAfterEpisodes':value},sources);
  }
  if(binding.codec==='tv-support'){
   if(value!=='none'&&value!=='hdr'&&value!=='surround'&&value!=='hdr-surround')throw new Error('Invalid setting value.');
   return {form:'device',values:{tvHdr:value==='hdr'||value==='hdr-surround',tvSurround:value==='surround'||value==='hdr-surround'}};
  }
  if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).length!==Object.keys(binding.fields).length)throw new Error('Invalid setting matrix.');
  const values=Object.fromEntries(Object.entries(binding.fields).map(([name,key])=>{const v=(value as Record<string,unknown>)[name];if(binding.key==='segments.libraryModes'?v!=='server'&&v!=='standard'&&v!=='thorough'&&v!=='off':v!=='ask'&&v!=='auto'&&v!=='off')throw new Error('Invalid setting matrix.');return [key,v];}));
  return binding.form==='preferences'?preferenceBindingPatch(binding,values,sources):{form:binding.form,values};
 }
 if(binding.codec==='missing-rule'){if(value==='never')return {form:binding.form,values:{'library.missingTitles':'never'}};if(value==='now')return {form:binding.form,values:{'library.missingTitles':'immediately'}};const custom=value&&typeof value==='object'&&!Array.isArray(value)&&Object.keys(value).length===1?(value as Record<string,unknown>).days:undefined;const n=value==='7d'?7:value==='30d'?30:value==='90d'?90:custom;if(typeof n!=='number'||!Number.isSafeInteger(n)||n<1||n>3650)throw new Error('Invalid missing-title retention days.');return {form:binding.form,values:{'library.missingTitles':'after_days','library.missingTitlesDays':n}};}
 let raw=value;
 if(binding.codec==='artwork-size'){
  if(value!=='small'&&value!=='standard'&&value!=='large')throw new Error('Invalid setting value.');
  raw=value==='small'?1280:value==='standard'?1920:2560;
 }
 if(binding.codec==='inherited-watched-threshold'){
  const field=sources.preferences?.registry.fields.find(f=>f.key===binding.key);
  if(field?.type!=='integer')throw new Error('Invalid setting value.');
  raw=value==='server'||value===null?null:value;
 }
 if(binding.codec==='music-quality-choice'){
  if(typeof value!=='string'||value==='cd-quality'||!canonicalMusicQualityLadder.some(q=>(q.id==='cd-quality'?'cd':q.id)===value))throw new Error('Invalid setting value.');
  raw=value==='cd'?'cd-quality':value;
 }
 if(binding.codec==='video-quality-choice'){
  if(typeof value!=='string'||value==='2160p'||!canonicalQualityLadder.some(q=>(q.id==='2160p'?'4k':q.id)===value)||binding.key==='downloadMaxQuality'&&value==='automatic')throw new Error('Invalid setting value.');
  raw=value==='4k'?'2160p':value;
 }
 if(binding.codec==='boolean'&&typeof value!=='boolean')throw new Error('Invalid setting value.');
 if(binding.codec==='insecure-connections'&&value!=='never'&&value!=='local'&&value!=='always')throw new Error('Invalid setting value.');
 if(binding.codec==='notice-audience'){if(!validNoticeAudience(value))throw new Error('Invalid setting matrix.');raw={...value};}
 if(binding.codec==='hardware-selection'&&!validHardwareSelection(value))throw new Error('Invalid setting value.');
 if(binding.codec==='surround'){if(value!=='asis'&&value!=='stereo')throw new Error('Invalid setting value.');raw=value==='stereo';}
 if(binding.codec==='player-engine'){if(value!=='auto'&&value!=='native'&&value!=='mpv')throw new Error('Invalid setting value.');raw=value==='auto'?'automatic':value;}
 if(binding.form==='preferences')return preferenceBindingPatch(binding,{[binding.key]:raw},sources);
 if(binding.key==='planningPolicy')raw=value==='least'?'minimize_server_work':value==='most'?'maximum_fidelity':undefined;
 if(value==='custom'&&binding.codec!=='window-mode')throw new Error('Custom is derived.');
 if(binding.codec==='domain'){
  if(value==='')raw='';else{if(typeof value!=='string')throw new Error('An HTTPS address is required.');const url=new URL(value.includes('://')?value:`https://${value}`);if(url.protocol!=='https:'||url.username||url.password||url.pathname!=='/'||url.search||url.hash||url.port)throw new Error('An HTTPS domain is required.');raw=url.hostname;}
 }else if(binding.codec==='update-checks'){raw=value==='never'?'':value==='auto'?defaultFeed(sources):undefined;}
 else if(binding.codec==='away-quality'){raw=value==='original'?0:canonicalQualityLadder.find(q=>q.height>0&&q.id===(value==='4k'?'2160p':value))?.videoKbps;}
 else if(binding.codec==='interface')raw=value==='all'?'':value;
 else if(binding.codec==='remote-policy')raw=value==='anyone'?'allow':value==='owner'?'owner-only':value==='none'?'off':undefined;
 else if(binding.codec==='unlimited')raw=value==='unlimited'||value==='auto'?(binding.key==='serverCap'?null:0):Number(value);
 else if(binding.codec==='history-retention'){
  raw=typeof value==='string'&&Object.hasOwn(historyRetentionDays,value)?historyRetentionDays[value]:typeof value==='number'||typeof value==='string'&&/^\d+$/.test(value)?Number(value):undefined;
  if(typeof raw!=='number'||!Number.isSafeInteger(raw)||raw<0||raw>36500)throw new Error('Invalid history retention days.');
 }
 else if(binding.codec==='number-choice')raw=Number(value);
 else if(binding.codec==='days'){
  if(!value||typeof value!=='object'||Object.keys(value).length!==7)throw new Error('Seven days are required.');
  const m=value as Record<string,unknown>;if(days.some(d=>m[d]!=='runs'&&m[d]!=='skips'))throw new Error('Invalid day.');raw=days.filter(d=>m[d]==='runs');
 }else if(binding.codec==='log-retention'){
  if(!value||typeof value!=='object'||Object.keys(value).some(k=>!categories.includes(k as typeof categories[number])))throw new Error('Invalid log category.');
  raw=Object.entries(value).flatMap(([category,days])=>{if(days==='default')return [];const n=Number(days);if(!Number.isSafeInteger(n)||n<1||n>90)throw new Error('Invalid log retention.');return [{category,days:n}];});
 }
 if(binding.scale)raw=value===null&&binding.key==='uploadCapacityKbps'?0:typeof raw==='number'?Math.round(raw*binding.scale):raw;
 if(typeof raw==='number'&&!Number.isFinite(raw)||raw===undefined)throw new Error('Invalid setting value.');
 if(binding.window){
  const all=windows(sources),existing=all.find(w=>matchingWindow(w,binding.window!));if(!existing)throw new Error('Load maintenance settings before editing a window.');let change:Record<string,unknown>={[binding.key]:raw};
  if(binding.codec==='window-mode'){
   if(binding.window==='heavy')change=value==='any'?{enabled:false}:value==='overnight'?{enabled:true,cadence:'nightly',startMinute:60,durationMinutes:300,days:[...days],timezone:''}:value==='custom'?{enabled:true}:{};
   else change=value==='never'?{enabled:false}:value==='custom'?{enabled:true}:typeof value==='string'&&cadences[value]?{enabled:true,cadence:value,days:[...cadences[value]!]}:{};
   if(!Object.keys(change).length)throw new Error('Invalid maintenance window.');
  }else if(binding.codec==='days')change={days:raw,cadence:'custom'};
  return {form:binding.form,values:{windows:all.map(w=>w.id===existing.id?{...w,...change}:w)}};
 }
 return {form:binding.form,values:{[binding.key]:raw}};
}
/** Apply dotted domain fields as well as literal dotted registry keys. */
export function applySettingsBindingPatch<T extends Readonly<Record<string,unknown>>>(draft:T,patch:SettingsBindingPatch):T{
 const next=clonePortableDto(draft) as Record<string,unknown>;
 for(const [key,value] of Object.entries(patch.values)){
  if(Object.hasOwn(next,key)||!key.includes('.')){next[key]=clonePortableDto(value);continue;}
  const parts=key.split('.');let at=next;for(const part of parts.slice(0,-1)){const child=at[part];at[part]=child&&typeof child==='object'&&!Array.isArray(child)?{...child}:{};at=at[part] as Record<string,unknown>;}at[parts.at(-1)!]=clonePortableDto(value);
 }
 return next as T;
}

export type SettingsPresetOption=Readonly<{value:unknown;sets?:Readonly<Record<string,unknown>>}>;
function equal(a:unknown,b:unknown):boolean{
 if(Object.is(a,b))return true;
 if(Array.isArray(a)&&Array.isArray(b))return a.length===b.length&&a.every((v,i)=>equal(v,b[i]));
 if(a&&b&&typeof a==='object'&&typeof b==='object'){const aa=a as Record<string,unknown>,bb=b as Record<string,unknown>;return Object.keys(aa).length===Object.keys(bb).length&&Object.keys(aa).every(k=>Object.hasOwn(bb,k)&&equal(aa[k],bb[k]));}
 return false;
}
/** Only full exact sets match; no nearest-choice policy loses Advanced values. */
function matches(actual:unknown,expected:unknown):boolean{if(expected&&typeof expected==='object'&&!Array.isArray(expected)){if(!actual||typeof actual!=='object'||Array.isArray(actual))return false;return Object.entries(expected).every(([key,value])=>matches((actual as Record<string,unknown>)[key],value));}return equal(actual,expected);}
export function exactPresetValue(options:readonly SettingsPresetOption[],read:(rowId:string)=>unknown):unknown{return options.find(option=>option.sets&&Object.entries(option.sets).every(([key,value])=>matches(read(key),value)))?.value??'custom';}
export function presetPatch(options:readonly SettingsPresetOption[],choice:unknown,read?:(rowId:string)=>unknown):Readonly<Record<string,unknown>>{const option=options.find(option=>Object.is(option.value,choice));if(!option?.sets)throw new Error('A stored preset is required.');return Object.fromEntries(Object.entries(option.sets).map(([key,value])=>[key,value&&typeof value==='object'&&!Array.isArray(value)?{...(read?.(key) as Record<string,unknown>??{}),...clonePortableDto(value)}:clonePortableDto(value)]));}
export const resetPreset=presetPatch;
export function changedFromDefault(current:unknown,defaultValue:unknown):boolean{return !equal(current,defaultValue);}

export {searchConfiguration} from '../settings-configuration.ts';

function libraryProviderMediaKind(kind:string):string{return kind==='tv'||kind==='anime'?'show':kind==='audiobook'?'book':kind;}
const libraryAnalysisRows=['local_metadata','subtitle_discovery','checksum','waveform','loudness','audio_fingerprint','trickplay','chapter_images'] as const;
function libraryRowValue(rowId:string,settings:LibrarySettings,doc:LibraryAdminDocument):unknown{
 const binding=libraryScalarBindings[rowId];if(binding)return presented(binding,at(settings,binding.key),{});
 switch(rowId){
 case 'libraries.scanDepth':return libraryScanDepth(settings);
 case 'libraries.analysisSteps':return Object.fromEntries(libraryAnalysisRows.filter(id=>doc.analysisMatrix.operations.some(op=>op.id===id)).map(id=>[id,settings.analysis.includes(id)?'run':'skip']));
 case 'libraries.scrub':{if(!settings.analysis.includes('trickplay'))return 'off';const n=settings.navigation;return n.trickplayIntervalMs===10000&&n.trickplayTileWidth===320?'standard':n.trickplayIntervalMs===5000&&n.trickplayTileWidth===320?'smooth':n.trickplayIntervalMs===20000&&n.trickplayTileWidth===240?'compact':'custom';}
 case 'libraries.chapterPictures':return settings.analysis.includes('chapter_images')&&settings.navigation.chapterThumbnailMode==='generated'?'generated':!settings.analysis.includes('chapter_images')&&settings.navigation.chapterThumbnailMode==='embedded'?'embedded':'custom';
 case 'libraries.scrubMax':{const hours=settings.navigation.trickplayMaxTiles*settings.navigation.trickplayIntervalMs/3600000;return [1,2,3,4,6].includes(hours)?String(hours):'custom';}
 case 'libraries.titlesFrom':return settings.providers.find(p=>p.mediaKind===libraryProviderMediaKind(doc.libraryKind))?.provider;
 case 'libraries.language':return settings.providers.find(p=>p.mediaKind===libraryProviderMediaKind(doc.libraryKind))?.language;
 case 'libraries.region':return settings.providers.find(p=>p.mediaKind===libraryProviderMediaKind(doc.libraryKind))?.region;
 default:return undefined;
 }
}
/** Only producer-eligible rows are exposed. Defaults require the producer's own defaults DTO. */
export function readLibrarySettingsBinding(rowId:string,document:LibraryAdminDocument&Readonly<{defaults?:LibrarySettings}>):SettingsBindingValue|undefined{
 if(!document.eligibleControls.includes(rowId))return undefined;const current=libraryRowValue(rowId,document.settings,document);if(current===undefined)return undefined;
 return {current,effective:current,default:document.defaults?libraryRowValue(rowId,document.defaults,document):undefined,source:'library',configOnly:false};
}
/** Read the separate Sonic domain through its current Library eligibility hint. */
export function readLibrarySonicSettingsBinding(rowId:string,document:Pick<LibraryAdminDocument,'libraryId'|'libraryKind'|'eligibleControls'>,sonic:LibrarySonicDocument):SettingsBindingValue|undefined{
 if(document.libraryKind!=='music'||document.libraryId!==sonic.libraryId||!sonic.available||typeof sonic.defaultEnabled!=='boolean'||!document.eligibleControls.includes(rowId))return undefined;
 if(rowId==='libraries.sonicAnalysis')return {current:sonic.enabled,effective:sonic.enabled,default:sonic.defaultEnabled,source:'library',configOnly:false};
 if(rowId==='libraries.sonicStatus'){
  const value=Object.freeze({state:sonic.state,done:sonic.done,total:sonic.total,observedAtMs:sonic.observedAtMs,...(sonic.reasonCode===undefined?{}:{reasonCode:sonic.reasonCode}),...(sonic.pausedUntilMs===undefined?{}:{pausedUntilMs:sonic.pausedUntilMs}),...(sonic.estimatedRemainingSeconds===undefined?{}:{estimatedRemainingSeconds:sonic.estimatedRemainingSeconds})});
  return {current:value,effective:value,default:undefined,source:'library',configOnly:false};
 }
 return undefined;
}
/** Sonic keeps its own settings revision and writer, separate from full Library Save. */
export function patchLibrarySonicSettingsBinding(rowId:string,value:unknown,document:Pick<LibraryAdminDocument,'libraryId'|'libraryKind'|'eligibleControls'>,sonic:LibrarySonicDocument):PutAdminLibrariesIdSonicAnalysisRequest{
 if(rowId!=='libraries.sonicAnalysis'||typeof value!=='boolean'||!readLibrarySonicSettingsBinding(rowId,document,sonic))throw new Error('Invalid or unavailable library Sonic setting.');
 return {enabled:value,expectedRevision:sonic.revision};
}

/** One complete typed library draft; the facade applies its revision/operation fence on Save. */
export function patchLibrarySettingsBinding(rowId:string,value:unknown,document:LibraryAdminDocument):LibrarySettings{
 if(!document.eligibleControls.includes(rowId))throw new Error('This library does not offer that setting.');if(value==='custom')throw new Error('Custom is derived.');const settings=document.settings,binding=libraryScalarBindings[rowId];
 if(binding)return applySettingsBindingPatch(settings,settingsBindingPatch(binding,value));
 switch(rowId){
 case 'libraries.scanDepth':if(value==='file_list_only'||value==='basic'||value==='complete')return setLibraryScanDepth(settings,value);break;
 case 'libraries.scrub':if(value==='off'||value==='standard'||value==='smooth'||value==='compact')return setLibrarySeekPreviews(settings,value);break;
 case 'libraries.chapterPictures':if(value==='generated'||value==='embedded')return setLibraryAnalysis(settings,document.analysisMatrix,'chapter_images',value==='generated');break;
 case 'libraries.scrubMax':{const n=Number(value);if(n===1||n===2||n===3||n===4||n===6)return setLibraryPreviewHours(settings,n);break;}
 case 'libraries.analysisSteps':{
  if(!value||typeof value!=='object'||Array.isArray(value))break;let next=settings;
  const entries=Object.entries(value);if(entries.some(([id,mode])=>!libraryAnalysisRows.includes(id as typeof libraryAnalysisRows[number])||!document.analysisMatrix.operations.some(op=>op.id===id)||mode!=='run'&&mode!=='skip'))break;
  // Disable dependent operations before adding the enabled dependency chains.
  for(const [id,mode] of entries)if(mode==='skip')next=setLibraryAnalysis(next,document.analysisMatrix,id,false);
  for(const [id,mode] of entries)if(mode==='run')next=setLibraryAnalysis(next,document.analysisMatrix,id,true);
  return next;
 }
 case 'libraries.titlesFrom':case 'libraries.language':case 'libraries.region':{
  if(typeof value!=='string')break;const index=settings.providers.findIndex(p=>p.mediaKind===libraryProviderMediaKind(document.libraryKind));if(index<0)throw new Error('Load this library’s provider selection before editing it.');
  if(rowId==='libraries.titlesFrom'&&!document.availableProviders.some(p=>p.id===value&&p.mediaKinds.includes(libraryProviderMediaKind(document.libraryKind))))break;
  const field=rowId==='libraries.titlesFrom'?'provider':rowId==='libraries.language'?'language':'region';return {...settings,providers:settings.providers.map((p,i)=>i===index?{...p,[field]:value}:p)} as LibrarySettings;
 }
 }
 throw new Error('Invalid library setting value.');
}


export const libraryCreateKinds=Object.freeze([
 {choice:'movies',kind:'movie'},{choice:'tv',kind:'tv'},{choice:'anime',kind:'anime'},{choice:'music',kind:'music'},
 {choice:'audiobooks',kind:'audiobook'},{choice:'books',kind:'book'},{choice:'comics',kind:'comic'},
] as const);
export function libraryKindForChoice(choice:string):(typeof libraryCreateKinds)[number]['kind']{const kind=libraryCreateKinds.find(c=>c.choice===choice)?.kind;if(!kind)throw new Error('Invalid library kind.');return kind;}
const libraryFolderFields:Readonly<Record<string,keyof SourceSettings>>={'libraries.folderPath':'path','libraries.folderName':'name','libraries.location':'classification','libraries.rescan':'intervalSeconds','libraries.symlinks':'followSymlinks'};
const folderIntervals:Readonly<Record<string,number>>={'15m':900,'1h':3600,'6h':21600,'1d':86400,'never':0};
export function readLibrarySourceSettingsBinding(rowId:string,source:InventorySource):SettingsBindingValue|undefined{
 const key=libraryFolderFields[rowId];if(!key||key==='expectedRevision'||key==='acceptReplacement')return undefined;
 const raw=source[key as keyof InventorySource];const current=rowId==='libraries.rescan'?Object.entries(folderIntervals).find(([,n])=>n===raw)?.[0]??'custom':raw;
 return {current,effective:current,default:undefined,source:'library-source',configOnly:false};
}
/** Revision and replacement consent remain the caller's current inventory intent. */
export function patchLibrarySourceSettingsBinding(rowId:string,value:unknown,draft:SourceSettings):SourceSettings{
 const key=libraryFolderFields[rowId];if(!key)throw new Error('Invalid library folder setting.');let raw=value;
 if(rowId==='libraries.rescan'){if(typeof value!=='string'||!Object.hasOwn(folderIntervals,value))throw new Error('Custom is derived.');raw=folderIntervals[value];}
 else if(rowId==='libraries.location'){if(value!=='local'&&value!=='network')throw new Error('Invalid folder location.');}
 else if(rowId==='libraries.symlinks'){if(typeof value!=='boolean')throw new Error('Invalid symbolic-link policy.');}
 else if(typeof value!=='string'||/[\u0000-\u001f]/.test(value))throw new Error('Invalid folder value.');
 return {...draft,[key]:raw};
}

function readPersonStreamChoice(mode: unknown, count: unknown): string {
 if (mode === 'inherit' && count === 0) return 'default';
 if (mode === 'unlimited' && count === 0) return 'unlimited';
 if (mode === 'limit' && typeof count === 'number' && Number.isSafeInteger(count) && count >= 1 && count <= 1000) return String(count);
 throw new Error('invalid_settings_stream_limit');
}
function personStreamPatch(choice: unknown): Pick<MemberLimits,'maxStreamsMode'|'maxStreams'> {
 if (choice === 'default') return {maxStreamsMode:'inherit',maxStreams:0};
 if (choice === 'unlimited') return {maxStreamsMode:'unlimited',maxStreams:0};
 if (typeof choice === 'string' && /^[1-9][0-9]{0,3}$/.test(choice) && Number(choice) <= 1000) return {maxStreamsMode:'limit',maxStreams:Number(choice)};
 throw new Error('invalid_settings_stream_limit');
}
/** Presentation only. Current server facts and the existing member/bulk CAS
 * writers retain authority; zero never identifies inheritance or unlimited. */
export const peopleStreamLimits = Object.freeze({
 read(person: Pick<AccessMember,'limits'>): string {
  return readPersonStreamChoice(person.limits.maxStreamsMode,person.limits.maxStreams);
 },
 patch(person: Pick<AccessMember,'limits'>,choice: unknown): MemberLimits {
  return {...person.limits,...personStreamPatch(choice)};
 },
 readBulk(access: SettingsBulkAccess | undefined): string {
  if (access?.maxStreamsMode === undefined && access?.maxStreams === undefined) return 'keep';
  return readPersonStreamChoice(access?.maxStreamsMode,access?.maxStreams);
 },
 patchBulk(access: SettingsBulkAccess | undefined,choice: unknown): SettingsBulkAccess {
  const next = {...access};
  if (choice === 'keep') {delete next.maxStreamsMode;delete next.maxStreams;return next;}
  return {...next,...personStreamPatch(choice)};
 }
});
