import React from 'react';
import {Text} from 'react-native';
import {enUS} from '../../../../../packages/i18n/src/index';
import {fontFamilies, useTheme} from '../theme';
import {stateSentenceParts} from './sentence-parts';

type Sentences = {
  'next.states.versionFailureBody': {failedVersion: string; position: string; alternative: string};
  'next.states.convertingOffBody': {title: string};
  'next.states.droppedBody': {position: string};
  'next.states.native.tvStoppedBody': {position: string};
};
export type StateSentenceId = keyof Sentences;
export type StateSentenceProps<K extends StateSentenceId = StateSentenceId> = Readonly<{messageId: K; values: Sentences[K]}>;
const strongArguments: Record<StateSentenceId, readonly string[]> = {
  'next.states.versionFailureBody': ['failedVersion', 'alternative'],
  'next.states.convertingOffBody': ['title'],
  'next.states.droppedBody': ['position'],
  'next.states.native.tvStoppedBody': ['position'],
};

/** A Text-compatible body slot from an approved catalogue sentence. */
export function StateSentence<K extends StateSentenceId>({messageId, values}: StateSentenceProps<K>) {
  const theme = useTheme();
  return <>{stateSentenceParts(enUS[messageId], values, strongArguments[messageId]).map((part, index) => part.strong
    ? <Text key={index} style={{fontFamily: fontFamilies[700], color: theme.colors.text}}>{part.text}</Text>
    : <React.Fragment key={index}>{part.text}</React.Fragment>)}</>;
}
