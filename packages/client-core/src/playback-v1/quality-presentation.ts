import {canonicalMusicQualityLadder, canonicalQualityLadder} from '../../../contracts/src/types.ts';
import type {MessageId} from '../../../i18n/src/index.ts';
import {serviceI18n} from '../presentation/service-text.ts';
import type {SavedMusicQuality, SavedQuality} from './quality.ts';

const labels: Readonly<Record<SavedQuality,MessageId>> = {
 original:'quality.original',automatic:'quality.automatic','2160p':'quality.4k',
 '1080p-high':'quality.1080pHigh','1080p-low':'quality.1080pLow',
 '720p-high':'quality.720pHigh','720p-low':'quality.720pLow',
 '480p':'quality.height','240p':'quality.height',
};
/** One presentation for player, download and headless saved-limit consumers.
 * Canonical numeric data remains exact; only the localized about hint rounds. */
export function qualityPresentation(id:SavedQuality, sameAsOriginal=false, i18n=serviceI18n()) {
 const step=canonicalQualityLadder.find(r=>r.id===id)!;
 const label=i18n.t(labels[id],{height:step.height});
 const amount=step.dataGBPerHour;
 const hint=sameAsOriginal?i18n.t('quality.sameAsOriginal'):amount===null?'':i18n.t('quality.dataPerHour',{amount:new Intl.NumberFormat(i18n.locale,{maximumFractionDigits:1}).format(amount)});
 return Object.freeze({id,label,hint,sameAsOriginal,dataGBPerHour:amount,videoBitrateKbps:step.videoKbps,height:step.height});
}
/** Existing numeric away-limit adapters consume the same canonical ceiling.
 * Original/Automatic add no fixed cap; owner/member caps remain independent. */
export function qualityLimitKbps(id:SavedQuality):number{return canonicalQualityLadder.find(r=>r.id===id)!.videoKbps;}

export function qualityLimit(id:SavedQuality){const q=canonicalQualityLadder.find(q=>q.id===id)!;return Object.freeze({maxVideoBitrateKbps:q.videoKbps,maxHeight:q.height});}

/** Music's saved ladder, with exact budgets and localized display rounding. */
export function musicQualityPresentation(id:SavedMusicQuality,sameAsOriginal=false,i18n=serviceI18n()) {
 const step=canonicalMusicQualityLadder.find(q=>q.id===id)!;
 const label=id==='cd-quality'?i18n.t('quality.cdQuality'):step.audioKbps?i18n.t('pref.unit.kbps',{value:step.audioKbps}):i18n.t('quality.original');
 const hint=sameAsOriginal?i18n.t('quality.sameAsOriginal'):step.audioKbps?i18n.t('quality.musicDataPerHour',{amount:new Intl.NumberFormat(i18n.locale,{maximumFractionDigits:0}).format(step.dataMBPerHour)}):'';
 return Object.freeze({...step,label,hint,sameAsOriginal});
}
