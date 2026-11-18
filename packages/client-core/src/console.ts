import type {FeedbackItemStatus} from './notifications.ts';

export type Page<T>={items:T[];nextCursor:string};
export type PlanningPolicy='maximum_fidelity'|'maximum_compatibility'|'minimize_server_work';
export type FeedbackStatus=FeedbackItemStatus;
export type Job={id:string;kind:string;resource:string;trigger:string;state:'queued'|'running'|'paused'|'reconciling'|'cancellation-requested'|'cancelled'|'failed'|'succeeded';phase:string;revision:number;attempt:number;createdAt:number;updatedAt:number;nextAt:number;domainId?:string;errorCode?:string;predecessor?:string;settingsRevision:number;lane:string;actions:('cancel'|'retry'|'pause'|'resume')[];processed?:number;total?:number};
export type Alert={params:Readonly<Partial<Record<'title'|'channel'|'reason'|'time',string>>>;id:string;code:string;severity:string;status:string;firstAt:number;lastAt:number;occurrences:number;revision:number};
export type Measurement={name:string;observedAt:number;freshUntil:number;facts:Record<string,{state:string;value:unknown;unit?:string;reason?:string}>};
export type EvidenceRecord={sequence:number;at:number;lane?:string;severity?:string;component?:string;code?:string;action?:string;target?:string;hash?:string;previousHash?:string;fields?:Record<string,number>};
export const SUPPORT_PATH_CATEGORIES=['library-configured-root','library-resolved-root','managed-mount-point'] as const;
export type SupportPathCategory=typeof SUPPORT_PATH_CATEGORIES[number];
export type SupportPathFacts=Readonly<{limit:128;hasMore:boolean;scanBudget:512;examined:number;scanTruncated:boolean;invalidAmongExamined:number;items:readonly Readonly<{category:SupportPathCategory;reference:string;value:string}>[]}>;
export type SupportExport={id:string;expiresAt:number;manifest:{version:string;from:number;to:number;included:string[];excluded:string[];redaction:string;maxBytes:number;includePaths?:boolean;pathCategories?:SupportPathCategory[];pathsTruncated?:boolean;pathsScanTruncated?:boolean;pathsInvalidAmongExamined?:number};snapshot?:unknown;snapshotPaths?:SupportPathFacts;runtime?:Page<EvidenceRecord>;client?:Page<EvidenceRecord>;audit?:Page<EvidenceRecord>};
