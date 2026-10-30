import test from 'node:test';
import assert from 'node:assert/strict';
import QRCode from 'qrcode';
import {qrMatrix} from '../src-next/kit/qr-matrix.ts';

// The invitation picture draws its code from qrMatrix: the real encoder's modules, row by row.
test('qrMatrix is the encoder module grid, square and row-major', () => {
  const link = 'https://getportico.tv/invite#7QK29MXPABCD1234';
  const rows = qrMatrix(link, 'M');
  const {size, data} = QRCode.create(link, {errorCorrectionLevel: 'M'}).modules;
  assert.equal(rows.length, size);
  assert.ok(rows.every(row => row.length === size));
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) assert.equal(rows[y]![x], !!data[y * size + x]);
  // Every QR code has a dark finder pattern in its top-left corner.
  assert.deepEqual(rows[0]!.slice(0, 7), [true, true, true, true, true, true, true]);
  assert.ok(qrMatrix(link, 'H').length >= size, 'a higher correction level never shrinks the code');
});
