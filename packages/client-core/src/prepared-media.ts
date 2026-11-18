/** A derivative is neither a catalog edition nor an original source-version ID. */
export type PreparedChoice = Readonly<{versionId: string; expectedRevision: number; offersRevision: string}>;
export type PreparedFacts = Readonly<{container: string; videoCodec: string; audioCodec: string; width: number; height: number; duration: number; audioTracks: number}>;
export type PreparedVersion = Readonly<{
  id: string; itemId: string; sourceId: string; profileId: string; targetId: string;
  sourceRevision: string; partIndex: number; editionId: string|null; digest: string;
  size: number; facts: PreparedFacts; state: 'published'|'deleting'|'deleted';
  revision: number; createdMs: number; selectable: boolean; reason: string;
}>;
