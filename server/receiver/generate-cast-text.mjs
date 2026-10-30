import {readFileSync, writeFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {createHash} from 'node:crypto';
import {receiverMessages} from '../../packages/i18n/src/catalog/en-US/cast-receiver.ts';
import {messageArguments} from '../../packages/i18n/src/messageformat.ts';

// Compile the canonical formatter; the receiver must not maintain its own ICU implementation.
const catalogue = new URL('../../packages/i18n/src/catalog/en-US/cast-receiver.ts', import.meta.url);
const formatter = new URL('../../packages/i18n/src/messageformat.ts', import.meta.url);
const output = new URL('./cast/receiver-text.js', import.meta.url);
const source = readFileSync(formatter, 'utf8');
const runtime = stripTypeScriptTypes(source).replace(/^export (class|function) /gm, '$1 ').replace(/[\t ]+$/gm, '');
if (/^\s*(?:import|export)\b/m.test(runtime)) throw new Error('Receiver formatter must be self-contained.');
for (const [key, message] of Object.entries(receiverMessages)) {
  if (!key.startsWith('cast.receiver.')) throw new Error('Invalid receiver catalogue key.');
  messageArguments(message); // Syntax is checked before the asset is written.
}
const strings = Object.fromEntries(Object.entries(receiverMessages).sort(([a], [b]) => a.localeCompare(b, 'en-US')));
const json = JSON.stringify(strings, null, 2).replace(/</g, '\\u003c');
const digest = file => createHash('sha256').update(readFileSync(file)).digest('hex');
const generated = `// Canonical receiver catalogue ${digest(catalogue)}; formatter ${digest(formatter)}.\n// Regenerate: node server/receiver/generate-cast-text.mjs\n(function () {\n'use strict';\n${runtime}\nconst messages = Object.freeze(Object.assign(Object.create(null), ${json}));\nwindow.porticoCastText = Object.freeze({\n  t: function (key, params) {\n    return Object.prototype.hasOwnProperty.call(messages, key)\n      ? formatMessage(messages[key], params || {}, {locale: 'en-US'}) : '';\n  }\n});\n})();\n`;
if (process.argv.includes('--check')) {
  if (readFileSync(output, 'utf8') !== generated) throw new Error('Receiver text asset is out of date.');
} else writeFileSync(output, generated);
