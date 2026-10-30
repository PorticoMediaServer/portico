export const sourceId = 'a'.repeat(48);
export const storedSource = {id: sourceId, name: 'Stored source', revision: 7, state: 'active', tunerCount: 2, tunerCountMode: 'configured', generation: 'b'.repeat(48), publishedAt: '', channels: 12, programmes: 20};
export const sourceReceipt = (id = sourceId) => ({protocolVersion: '1.0', serverId: 'server', source: {...storedSource, id}});
export const document = {protocolVersion: '1.0', serverId: 'server', sources: [storedSource], refresh: []};
export const uploadPreview = {protocolVersion: '1.0', serverId: 'server', preview: {channels: 1, programmes: 0, names: ['Stored channel']}};
export const remotePreview = {protocolVersion: '1.0', serverId: 'server', remotePreview: {previewId: sourceId, preview: {channels: 1, programmes: 0, availableStart: '', availableEnd: '', names: ['Stored channel'], diagnostics: {skippedChannels: 0, skippedProgrammes: 0, adjustedProgrammes: 0, examples: []}}, discoveredCapacity: 0, capacityMode: 'defaulted', mappingKeys: [{channelKey: 'channel', name: 'Stored channel', guideKey: ''}], expiresAt: '2099-01-01T00:00:00Z'}};
export const actionReceipt = {protocolVersion: '1.0', serverId: 'server', saved: true, removed: true, scheduled: true};
