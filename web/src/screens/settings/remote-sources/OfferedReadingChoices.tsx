import {defaultI18n, type MessageId} from '@i18n';
import {RadioGroup} from 'radix-ui';
import {sourceChoiceNames, type SourceChoiceDefinition} from '@core/remote-source-choice.ts';
import {MetadataFacts} from '../../../kit/MetadataFacts';
import styles from './offered-reading-choices.module.css';

const t = defaultI18n.t;
export const offeredChoiceLabels = {
  list: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.0.label',
  once: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.1.label',
  everything: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.2.label',
  ask: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.3.label',
} as const satisfies Record<(typeof sourceChoiceNames)[number], MessageId>;
export const offeredChoiceHints = {
  list: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.0.hint',
  once: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.1.hint',
  everything: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.2.hint',
  ask: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.3.hint',
} as const satisfies Record<(typeof sourceChoiceNames)[number], MessageId>;

/** The four canonical choice definitions the service actually published, drawn as the
 * authored cards. Selection is the exact definition object this render drew; the
 * callback closes that object and never reconstructs a preset. Consent and the
 * retired whole-file flag are not drawn, and scan depth is never presented as a
 * claim that heavy analysis will run. */
export function OfferedReadingChoices({definitions, selected, disabled, onSelect, phone = false}: Readonly<{
  definitions: readonly SourceChoiceDefinition[];
  selected?: SourceChoiceDefinition;
  disabled: boolean;
  onSelect: (definition: SourceChoiceDefinition) => void;
  phone?: boolean;
}>) {
  const offered = new Map(definitions.map(definition => [definition.name, definition] as const));
  return <RadioGroup.Root className={styles.choices} data-phone={phone || undefined} aria-label={t('next.remoteSources.readingMode')} disabled={disabled}
    value={selected && offered.get(selected.name) === selected ? selected.name : ''} onValueChange={value => {
      const name = sourceChoiceNames.find(name => name === value), definition = name && offered.get(name);
      if (!disabled && definition) onSelect(definition);
    }}>
    {sourceChoiceNames.map(name => {
      const definition = offered.get(name);
      if (!definition) return null;
      const checked = selected === definition;
      return <RadioGroup.Item key={name} value={name} disabled={disabled}
        className={styles.choice} data-picked={checked || undefined}>
        <span className={styles.radio} aria-hidden="true"/>
        <span className={styles.body}>
          <span className={styles.title}>{t(offeredChoiceLabels[name])}
            {name === 'once' && <span className={styles.tag}>{t('next.remoteSources.choiceRecommended')}</span>}
          </span>
          <span className={styles.hint}>{t(offeredChoiceHints[name])}</span>
          <span className={styles.facts}>
            <MetadataFacts parts={[<span><em>{t('next.remoteSources.factConnections')}</em>{definition.reading.connections}</span>,
              <span>{definition.reading.requestsPerSecond}{' '}{t('next.remoteSources.aSecond')}</span>]}/>
          </span>
        </span>
      </RadioGroup.Item>;
    })}
  </RadioGroup.Root>;
}
