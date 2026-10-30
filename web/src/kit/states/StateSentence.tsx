import {Fragment} from 'react';
import {catalogue, defaultI18n, enUS, parseMessage, type SupportedLocale} from '@i18n';

type SentenceValues = {
  'next.states.versionFailureBody': {failedVersion: string; position: string; alternative: string};
  'next.states.convertingOffBody': {title: string};
  'next.states.droppedBody': {position: string};
};
export type StateSentenceId = keyof SentenceValues;
export type StateSentenceProps<K extends StateSentenceId = StateSentenceId> = Readonly<{messageId: K; values: SentenceValues[K]; locale?: SupportedLocale}>;
const emphasis: Readonly<Record<StateSentenceId, readonly string[]>> = {
  'next.states.versionFailureBody': ['failedVersion','alternative'],
  'next.states.convertingOffBody': ['title'],
  'next.states.droppedBody': ['position'],
};

/** Catalogue argument boundaries preserve the emphasis drawn in states 8–10. No HTML or sentence splitting. */
export function StateSentence<K extends StateSentenceId>({messageId, values, locale = defaultI18n.region.uiLocale}: StateSentenceProps<K>) {
  const supplied = values as Readonly<Record<string,string>>;
  return <>{parseMessage(catalogue(locale)[messageId] ?? enUS[messageId]).map((node,index) => {
    if (node.kind === 'text') return <Fragment key={index}>{node.value}</Fragment>;
    if (node.kind === 'arg') return emphasis[messageId].includes(node.name) ? <strong key={index}>{supplied[node.name]}</strong> : <Fragment key={index}>{supplied[node.name]}</Fragment>;
    throw new TypeError('State sentences require text and named arguments.');
  })}</>;
}
