import test from 'node:test';
import assert from 'node:assert/strict';
import {buildLicenceData, licenceFiles, lockPackages, normaliseText, packagePathOf, podSources, productionPackages, readDependencyList, readPodfileLock, readPom, serialiseLicenceData, spdxFromMavenName, splitCopyright} from '../src/licences/inventory.ts';
import {inBuild, licenceCount, licenceDetail, licenceParagraphs, licenceRowDetail, licenceSections, licenceSourceRows, parseLicenceData, type LicenceData} from '../src/presentation/licences.ts';

const MIT = 'Permission is hereby granted, free of charge, to any person obtaining a copy of this software.\n\nTHE SOFTWARE IS PROVIDED "AS IS".';

test('copyright lines at the head are split off; the shared wording is what is left', () => {
  const a = splitCopyright(`MIT License\r\n\r\nCopyright (c) 2020 Ada  \r\nCopyright (c) 2021 Bob\r\n\r\n${MIT}\r\n`);
  assert.deepEqual(a.copyright, ['Copyright (c) 2020 Ada', 'Copyright (c) 2021 Bob']);
  assert.equal(a.body, `MIT License\n\n${MIT}`);
  const b = splitCopyright(`The MIT License\n\n© 2019 Carol\n\n${MIT}`);
  assert.deepEqual(b.copyright, ['© 2019 Carol']);
  // "All rights reserved." joins the line it belongs to.
  assert.deepEqual(splitCopyright('Copyright (c) 2009, Google Inc.\nAll rights reserved.\n\nRedistribution and use in source and binary forms.').copyright, ['Copyright (c) 2009, Google Inc. All rights reserved.']);
});

test('copyright wording inside a licence body stays where it is', () => {
  const apache = '                                 Apache License\n                           Version 2.0, January 2004\n\n   1. Definitions.\n\n      copyright owner or by an individual or Legal Entity authorized to submit.\n      Copyright notice text that is part of the licence itself.';
  const split = splitCopyright(apache);
  assert.deepEqual(split.copyright, []);
  assert.match(split.body, /copyright owner/);
  assert.match(split.body, /Copyright notice text/);
});

test('the GNU licences keep the Free Software Foundation line as part of their text', () => {
  const gpl = '                    GNU GENERAL PUBLIC LICENSE\n                       Version 3, 29 June 2007\n\n Copyright (C) 2007 Free Software Foundation, Inc. <http://fsf.org/>\n Everyone is permitted to copy and distribute verbatim copies.';
  assert.deepEqual(splitCopyright(gpl).copyright, []);
  assert.match(splitCopyright(gpl).body, /Free Software Foundation/);
});

test('identical texts are stored once, components sorted by section and name, output stable', () => {
  const data = buildLicenceData({
    client: 'web',
    sources: [{id: 'portico', url: 'https://example.invalid/src'}],
    inputs: {'z.lock': '2', 'a.lock': '1'},
    components: [
      {id: 'npm:zed@1.0.0', name: 'zed', version: '1.0.0', licence: 'MIT', group: 'packages', text: `Copyright (c) Zed\n\n${MIT}`},
      {id: 'npm:Alpha@2.0.0', name: 'Alpha', version: '2.0.0', licence: 'MIT', group: 'packages', text: `Copyright (c) Alpha\n\n${MIT}`},
      {id: 'font:manrope', name: 'Manrope', licence: 'OFL-1.1', group: 'fonts', copyright: ['Copyright 2018 The Manrope Project Authors']},
      {id: 'portico', name: 'Portico', licence: 'GPL-3.0-or-later', group: 'portico', text: 'GNU GENERAL PUBLIC LICENSE\nVersion 3'},
    ],
  });
  assert.deepEqual(data.components.map(c => c.id), ['portico', 'npm:Alpha@2.0.0', 'npm:zed@1.0.0', 'font:manrope']);
  assert.equal(data.texts.length, 2);
  assert.equal(data.components[1]!.text, data.components[2]!.text);
  assert.deepEqual(data.components[2]!.copyright, ['Copyright (c) Zed']);
  assert.deepEqual(data.components[3]!.copyright, ['Copyright 2018 The Manrope Project Authors']);
  assert.equal(data.components[3]!.text, undefined);
  assert.deepEqual(Object.keys(data.inputs), ['a.lock', 'z.lock']);
  assert.ok(serialiseLicenceData(data).endsWith('}\n'));
  assert.throws(() => buildLicenceData({client: 'x', sources: [], inputs: {}, components: [data.components[1]! as never, data.components[1]! as never]}), /listed twice/);
});

test('production packages leave out dev tooling, workspace links and type declarations', () => {
  const packages = lockPackages({packages: {
    '': {name: 'app'},
    'node_modules/react': {version: '19.2.8', license: 'MIT'},
    'node_modules/vite': {version: '8.2.2', dev: true},
    'node_modules/fsevents': {version: '2.3.3', devOptional: true},
    'node_modules/@types/qrcode': {version: '1.5.6'},
    'node_modules/@portico/design': {link: true, resolved: 'packages/design'} as never,
    'node_modules/a/node_modules/b': {version: '1.0.0'},
  }});
  assert.deepEqual(productionPackages(packages).map(p => `${p.name}@${p.version}`), ['react@19.2.8', 'b@1.0.0']);
  assert.equal(packagePathOf('node_modules/react/cjs/react.production.js'), 'node_modules/react');
  assert.equal(packagePathOf('node_modules/@babel/runtime/helpers/x.js'), 'node_modules/@babel/runtime');
  assert.equal(packagePathOf('node_modules/a/node_modules/b/index.js'), 'node_modules/a/node_modules/b');
  assert.equal(packagePathOf('src/app.ts'), undefined);
  assert.deepEqual(licenceFiles(['index.js', 'NOTICE', 'LICENSE-MIT', 'LICENSE.md', 'COPYING']), ['LICENSE.md', 'LICENSE-MIT', 'COPYING', 'NOTICE']);
});

test('Maven licence names and POM fields', () => {
  assert.equal(spdxFromMavenName('The Apache Software License, Version 2.0'), 'Apache-2.0');
  assert.equal(spdxFromMavenName('The Apache License, Version 2.0'), 'Apache-2.0');
  assert.equal(spdxFromMavenName('Apache-2'), 'Apache-2.0');
  assert.equal(spdxFromMavenName('MIT License'), 'MIT');
  assert.equal(spdxFromMavenName('Android Software Development Kit License'), 'Android Software Development Kit License');
  const pom = readPom('<project><parent><groupId>g</groupId><artifactId>p</artifactId><version>1</version></parent><url>https://x.invalid</url><organization><name>Org</name></organization><licenses><license><name>MIT</name><url>u</url></license></licenses></project>');
  assert.equal(pom.licenceUrl, 'u');
  assert.deepEqual(pom.licences, ['MIT']);
  assert.equal(pom.organisation, 'Org');
  assert.equal(pom.url, 'https://x.invalid');
  assert.deepEqual(pom.parent, {group: 'g', artifact: 'p', version: '1'});
  assert.deepEqual(readDependencyList('# pinned\nandroidx.core:core:1.16.0\n\n'), [{group: 'androidx.core', artifact: 'core', version: '1.16.0'}]);
  assert.throws(() => readDependencyList('broken'), /group:artifact:version/);
});

test('Podfile.lock pods and their sources', () => {
  const lock = 'PODS:\n  - boost (1.84.0)\n  - hermes-engine (0.14.1):\n    - hermes-engine/Pre-built (= 0.14.1)\n  - hermes-engine/Pre-built (0.14.1)\n  - "RCT-Folly/Default (2024.11.18.00)":\n    - boost\n\nDEPENDENCIES:\n  - boost (from `x`)\n\nEXTERNAL SOURCES:\n  boost:\n    :podspec: "../node_modules/react-native/third-party-podspecs/boost.podspec"\n  React-Core:\n    :path: "../node_modules/react-native/"\n  RNSVG:\n    :path: "../node_modules/react-native-svg"\n\nCOCOAPODS: 1.16.2\n';
  assert.deepEqual(readPodfileLock(lock), [{name: 'boost', version: '1.84.0'}, {name: 'hermes-engine', version: '0.14.1'}, {name: 'RCT-Folly', version: '2024.11.18.00'}]);
  assert.deepEqual(podSources(lock).get('boost'), {podspec: '../node_modules/react-native/third-party-podspecs/boost.podspec'});
  assert.deepEqual(podSources(lock).get('React-Core'), {path: '../node_modules/react-native/'});
  // A pod whose name is all capitals is a pod, not the start of the next section.
  assert.deepEqual(podSources(lock).get('RNSVG'), {path: '../node_modules/react-native-svg'});
});

const sample: LicenceData = {
  schema: 1, client: 'android', inputs: {},
  sources: [{id: 'portico-apps', url: 'https://example.invalid/apps'}, {id: 'media-engine', url: 'https://example.invalid/releases'}],
  texts: ['GPL', 'MIT body', 'Apache body'],
  components: [
    {id: 'portico', name: 'Portico', licence: 'GPL-3.0-or-later', group: 'portico', text: 0},
    {id: 'npm:react@19', name: 'react', version: '19', licence: 'MIT', group: 'packages', text: 1, copyright: ['Copyright (c) Meta']},
    {id: 'maven:cast', name: 'play-services-cast', version: '22', licence: 'Android Software Development Kit License', group: 'android', only: ['google']},
    {id: 'maven:core', name: 'core', version: '1.16.0', licence: 'Apache-2.0', group: 'android', text: 2},
  ],
};

test('sections in their fixed order, filtered to the build', () => {
  assert.deepEqual(licenceSections(sample).map(s => [s.group, s.components.length]), [['portico', 1], ['packages', 1], ['android', 2]]);
  assert.deepEqual(licenceSections(sample, 'firetv').map(s => [s.group, s.components.length]), [['portico', 1], ['packages', 1], ['android', 1]]);
  assert.equal(licenceSections(sample)[2]!.title, 'licenses.section.android');
  assert.equal(licenceCount(sample), 3);
  assert.equal(licenceCount(sample, 'firetv'), 2);
  assert.equal(inBuild(sample.components[2]!, 'google'), true);
  assert.equal(licenceRowDetail(sample.components[1]!), '19 · MIT');
  assert.deepEqual(licenceSourceRows(sample).map(r => r.label), ['licenses.source.apps', 'licenses.source.mediaEngine']);
});

test('a detail carries the copyright lines and the shared text', () => {
  assert.deepEqual(licenceDetail(sample, 'npm:react@19'), {component: sample.components[1], copyright: ['Copyright (c) Meta'], text: 'MIT body'});
  assert.deepEqual(licenceDetail(sample, 'maven:cast'), {component: sample.components[2], copyright: []});
  assert.equal(licenceDetail(sample, 'nope'), undefined);
});

test('a long text becomes paragraphs a remote can move through', () => {
  assert.deepEqual(licenceParagraphs('One.\n\n\nTwo\nlines.\n  \nThree.'), ['One.', 'Two\nlines.', 'Three.']);
  const long = Array.from({length: 40}, (_, i) => `line ${i} ${'x'.repeat(50)}`).join('\n');
  const parts = licenceParagraphs(long, 600);
  assert.ok(parts.length > 1 && parts.every(p => p.length <= 600));
  assert.equal(parts.join('\n'), long);
});

test('only a well-formed file is drawn', () => {
  assert.equal(parseLicenceData(sample), sample);
  assert.equal(parseLicenceData({schema: 2}), undefined);
  assert.equal(parseLicenceData(null), undefined);
  assert.equal(normaliseText('﻿a \r\nb\t\n\n'), 'a\nb');
});
