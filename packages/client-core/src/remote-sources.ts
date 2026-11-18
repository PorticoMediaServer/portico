import type {SourceChoiceBaseline, SourceChoiceDefinition, SourceChoiceField} from './remote-source-choice.ts';

export type DAVConfiguration=Readonly<{name:string;root:string;username:string;password:string;keepPassword:boolean;keepConnection:boolean;insecureLocal:boolean;expectedGeneration?:number}>;

/** The read-authority part of one source's Reading settings. A missing saved profile is
 * reported as `saved:false` with the server's list/once defaults; it is never invented. */
export type SourceReadValues=Readonly<{scanDepth:'file_list_only'|'basic'|'complete';wholeFileReads:boolean;connections:number;requestsPerSecond:number;bytesPerSecond:number;/** Recorded advanced media-analysis consent; absent means this server never published one, not false. */allowAdvancedMediaAnalysis?:boolean}>;
export type SourceReadSettings=Readonly<{sourceId:string;generation:string;revision:number;saved:boolean;values:SourceReadValues;
 /** Present only when the server observed an effective configuration generation. */
 configurationGeneration?:string;
 /** Root57 projection, present only as the server published it. */
 choiceDefinitions?:readonly SourceChoiceDefinition[];
 choiceApplicableFields?:readonly SourceChoiceField[];
 choiceBaseline?:SourceChoiceBaseline}>;

/** The drawn Advanced page: how a source's folders are checked, the disk its
 * read cache may use, and which analysis stages run for files found through it. */
export type SourceAdvanced=Readonly<{scanIntervalSeconds:number;watchForChanges:boolean;scanOnStart:boolean;fullRecheckDays:number;cacheBytes:number;cacheFloorBytes:number;seekPreviews:boolean;chapterImages:boolean;waveforms:boolean;loudness:boolean;checksums:boolean;introsMode:'off'|'inherit'|'sound'|'picture';doubleCheckFolders:boolean;dirCacheSeconds:number;readBufferBytes:number;connectTimeoutSeconds:number;idleTimeoutSeconds:number;retries:number;lowLevelRetries:number}>;
export type SourceAdvancedSettings=Readonly<{sourceId:string;generation:string;revision:number;saved:boolean;cacheSupported:boolean;cacheApplied:boolean;cacheError?:string;cacheUsedBytes:number;scanIntervalObserved:number;values:SourceAdvanced}>;
