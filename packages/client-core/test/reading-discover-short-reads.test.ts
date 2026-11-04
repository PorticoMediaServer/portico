import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type Viewer} from '../src/index.ts';

// A Books library's Discover as a real server answers it (fixture capture, 4 Nov): the server counts
// words in EPUBs and PDFs alike, so a short read may be either (reading/discover_short_reads.go).
const library = "5LzVP_f2tcmMTvHUDt00vtlms0bMti-t2ZYExeDt8gI";
const body = {"rows": [{"key": "recent", "items": [{"coverUrl": "/v1/items/n62ipDOuyMhGuO3zgMfVng/art/cover?v=25ee3a7f38ed599137b1affb5ea6b044e17a3abf0ae0ddaebbda4eb4f2ab0c29&sd=533x800.50730.230ceaaad6fc7f70&ld=600x900.62554.7b85c922ec92d024", "id": "n62ipDOuyMhGuO3zgMfVng", "libraryId": "5LzVP_f2tcmMTvHUDt00vtlms0bMti-t2ZYExeDt8gI", "title": "A Map of Small Rooms", "format": "epub", "sourceId": "Lj61ijiLZxd4Qb65BlQklxum7mZGLvti5WAbyNeyKbc", "inventoryAvailable": true, "author": {"value": "Ivo Perrin", "source": "file-name"}}, {"id": "7OcYnBRvWsCGO0BmzCJ7Hw", "libraryId": "5LzVP_f2tcmMTvHUDt00vtlms0bMti-t2ZYExeDt8gI", "title": "A Short Atlas", "format": "pdf", "sourceId": "SbFvtzdXuRTwEYH-HCe1kNCL1RR9yweVEYOnK-sNFuI", "inventoryAvailable": true, "author": {"value": "Mara Vell", "source": "file-metadata"}}]}, {"key": "short-reads", "items": [], "shortReads": [{"item": {"coverUrl": "/v1/items/n62ipDOuyMhGuO3zgMfVng/art/cover?v=25ee3a7f38ed599137b1affb5ea6b044e17a3abf0ae0ddaebbda4eb4f2ab0c29&sd=533x800.50730.230ceaaad6fc7f70&ld=600x900.62554.7b85c922ec92d024", "id": "n62ipDOuyMhGuO3zgMfVng", "libraryId": "5LzVP_f2tcmMTvHUDt00vtlms0bMti-t2ZYExeDt8gI", "title": "A Map of Small Rooms", "format": "epub", "sourceId": "Lj61ijiLZxd4Qb65BlQklxum7mZGLvti5WAbyNeyKbc", "inventoryAvailable": true, "author": {"value": "Ivo Perrin", "source": "file-name"}}, "wordCount": 18284, "estimatedSeconds": 4389}, {"item": {"id": "7OcYnBRvWsCGO0BmzCJ7Hw", "libraryId": "5LzVP_f2tcmMTvHUDt00vtlms0bMti-t2ZYExeDt8gI", "title": "A Short Atlas", "format": "pdf", "sourceId": "SbFvtzdXuRTwEYH-HCe1kNCL1RR9yweVEYOnK-sNFuI", "inventoryAvailable": true, "author": {"value": "Mara Vell", "source": "file-metadata"}}, "wordCount": 1283, "estimatedSeconds": 308}]}]};
const viewer: Viewer = {accountId: 'a', profileId: 'p', serverId: 's', authority: 'local', role: 'member'};

test('a PDF short read is drawn, not a reason to refuse the whole Discover (Books › Discover drew a bare grid)', async () => {
  const api = new HttpLocalApi('http://127.0.0.1:1', 'throwaway-test-token', async () => new Response(JSON.stringify(body), {headers: {'Content-Type': 'application/json'}}));
  const client = api.reading({viewer, isCurrent: () => true});
  try {
    const page = await client.discover({libraryId: library, limit: 20, shortReadsLimit: 12});
    assert.deepEqual(page.rows.map(row => row.key), ['recent', 'short-reads']);
    assert.deepEqual(page.rows[1].shortReads?.map(entry => entry.item.format), ['epub', 'pdf']);
  } finally {client.dispose();}
});

test('a short read in a format with no word count is still refused', async () => {
  const bad = structuredClone(body); bad.rows[1].shortReads![1].item.format = 'cbz';
  const api = new HttpLocalApi('http://127.0.0.1:1', 'throwaway-test-token', async () => new Response(JSON.stringify(bad), {headers: {'Content-Type': 'application/json'}}));
  const client = api.reading({viewer, isCurrent: () => true});
  try {await assert.rejects(client.discover({libraryId: library, limit: 20, shortReadsLimit: 12}), /invalid_document/);}
  finally {client.dispose();}
});
