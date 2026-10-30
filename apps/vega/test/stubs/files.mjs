const records = new Map();
export const KeplerFileSystem = {
  exists: async path => records.has(path),
  readFileAsString: async path => records.get(path),
  writeStringToFile: async (path, text) => {records.set(path, text);},
  removeFile: async path => {records.delete(path);},
};
