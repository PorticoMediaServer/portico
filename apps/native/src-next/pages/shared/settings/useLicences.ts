import {useCallback, useEffect, useMemo, useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {licenceCount, licenceDetail, licenceParagraphs, licenceRowDetail, licenceSections, licenceSourceRows, type LicenceData} from '../../../../../../packages/client-core/src/presentation/licences';
import {loadLicences, licenceVariant} from '../../../platform/licences';
import {registerBack} from '../../../kit/navigation/back';

import type {LicenceRow} from '../../../kit/settings/LicenceRows';

/** Generated notices are read after mount; only nearby rows/paragraphs are mounted. */
export function useLicences({onBack, current}: {onBack: () => void; current: () => boolean}) {
  const t = defaultI18n.t;
  const [data, setData] = useState<LicenceData | null>(), [selected, setSelected] = useState<string>();
  useEffect(() => {
    const timer = setTimeout(() => {
      if (!current()) return;
      try {setData(loadLicences() ?? null);} catch {setData(null);}
    }, 0);
    return () => clearTimeout(timer);
  }, [current]);
  const back = useCallback(() => {if (!current()) return; if (selected) setSelected(undefined); else onBack();}, [current, selected, onBack]);
  useEffect(() => {
    const subscription = registerBack(() => {if (!current()) return false; back(); return true;}, {scope: 'page', active: current});
    return () => subscription();
  }, [current, back]);
  const detail = useMemo(() => data && selected ? licenceDetail(data, selected) : undefined, [data, selected]);
  const rows = useMemo((): LicenceRow[] => {
    if (!data) return [];
    if (detail) return [
      {id: 'facts', title: detail.component.name, body: licenceRowDetail(detail.component)},
      ...(detail.component.url ? [{id: 'website', title: t('licenses.website'), body: detail.component.url}] : []),
      ...detail.copyright.map((body, index) => ({id: `copyright-${index}`, title: index === 0 ? t('licenses.copyright') : '', body})),
      {id: 'text', title: t('licenses.text'), heading: true},
      ...(detail.text ? licenceParagraphs(detail.text).map((body, index) => ({id: `paragraph-${index}`, title: '', body}))
        : [{id: 'missing', title: '', body: t(detail.component.textAtUrl ? 'licenses.textAtWebsite' : 'licenses.textMissing')}]),
    ];
    const sections = licenceSections(data, licenceVariant()), portico = sections.find(section => section.group === 'portico');
    const componentRows = (section: typeof sections[number]): LicenceRow[] => [
      {id: `section-${section.group}`, title: t(section.title), heading: true},
      ...section.components.map(component => ({id: component.id, title: component.name, body: licenceRowDetail(component), componentId: component.id})),
    ];
    return [
      ...(portico ? componentRows(portico) : []),
      {id: 'source-title', title: t('licenses.source.title'), heading: true},
      ...licenceSourceRows(data).map(source => ({id: `source-${source.id}`, title: t(source.label), body: source.url})),
      {id: 'count', title: t('licenses.count', {count: licenceCount(data, licenceVariant())})},
      ...sections.filter(section => section.group !== 'portico').flatMap(componentRows),
    ];
  }, [data, detail, t]);
  const select = (component: string) => {if (current()) setSelected(component);};
  return {t, data, selected, title: detail?.component.name ?? t('licenses.title'), rows, select, back};
}
