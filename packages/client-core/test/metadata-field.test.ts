import test from 'node:test';
import assert from 'node:assert/strict';
import {metadataFieldError, metadataFieldLabel} from '../src/presentation/metadata-field.ts';
import type {RepairFieldSpec} from '../src/metadata-repair.ts';

const number: RepairFieldSpec = {field: 'number', label: 'web.metadata.field.year', group: 'numbers', type: 'integer', bulk: false, min: 0, max: 99999};

test('a field label in the catalogue reads in the app words; an unknown label stays as the server sent it', () => {
  assert.notEqual(metadataFieldLabel(number), 'web.metadata.field.year');
  assert.equal(metadataFieldLabel({...number, label: 'Number in series'}), 'Number in series');
});

test('a whole-number field refuses words, fractions and values beyond its bounds before Save, and allows empty', () => {
  for (const bad of ['abc', '1.5', '-1', '100000', ' 2']) assert.notEqual(metadataFieldError(number, bad), '', bad);
  for (const good of ['', '0', '1', '99999']) assert.equal(metadataFieldError(number, good), '', good);
});
