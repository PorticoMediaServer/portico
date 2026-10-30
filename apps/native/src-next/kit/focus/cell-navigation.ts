/** Semantic native destinations. No key handler or imperative focus movement lives here. */
export type FocusCell = Readonly<{index?: number; generation?: string; row?: number; kind?: 'channel' | 'programme'; boundary?: boolean; pending?: boolean; start?: number; end?: number}>;
export type CellDirection = 'up' | 'down' | 'left' | 'right';
export type CellMember = Readonly<{id: string; cell: FocusCell}>;
export type CellEdges = Readonly<Partial<Record<CellDirection, string>>>;
export type CellNavigation = Readonly<{grid?: Readonly<{columns: number; count?: number; shortRow?: 'clamp' | 'exit'}>;
  timeGrid?: Readonly<{initialTime: number; start: number; end: number}>; edges?: CellEdges}>;
export type CellDestination = Readonly<{childId: string}> | Readonly<{regionId: string}>;
const self = (id: string): CellDestination => ({childId: id});
const edge = (id: string, regionId: string | undefined): CellDestination => regionId ? {regionId} : self(id);

export function createCellNavigator(members: readonly CellMember[], config: CellNavigation,
  memory: Readonly<{column?: number; time?: number}>) {
  const byId = new Map(members.map(member => [member.id, member.cell]));
  const known = new Map(members.filter(member => member.cell.index !== undefined).map(member => [member.cell.index!, member.id]));
  const rows = new Map<number, {channel?: CellMember; programmes: CellMember[]}>();
  for (const member of members) {
    if (member.cell.row === undefined) continue;
    const row = rows.get(member.cell.row) ?? {programmes: []};
    if (member.cell.kind === 'channel') row.channel = member; else if (member.cell.kind === 'programme') row.programmes.push(member);
    rows.set(member.cell.row, row);
  }
  const positions = new Map<string, number>();
  for (const row of rows.values()) {
    row.programmes.sort((a, b) => (a.cell.start ?? 0) - (b.cell.start ?? 0));
    row.programmes.forEach((member, index) => positions.set(member.id, index));
  }
  return (id: string): Partial<Record<CellDirection, CellDestination>> => {
    const current = byId.get(id);
    if (!current) return {};
    if (config.grid && current.index !== undefined) {
      const columns = Math.max(1, Math.floor(config.grid.columns)), index = current.index;
      const row = Math.floor(index / columns), column = index % columns;
      const at = (value: number) => self(known.get(value) ?? id);
      const vertical = (direction: -1 | 1): CellDestination => {
        const nextRow = row + direction;
        if (nextRow < 0) return edge(id, config.edges?.up);
        const first = nextRow * columns;
        if (config.grid!.count !== undefined && first >= config.grid!.count!) return edge(id, config.edges?.down);
        const last = config.grid!.count === undefined ? first + columns - 1 : Math.min(first + columns - 1, config.grid!.count! - 1);
        const target = first + Math.min(columns - 1, memory.column ?? column);
        // A deliberate hole in a keypad exits to its actual neighboring region.
        // Poster grids keep the existing short-row column clamp by default.
        if (target > last && config.grid!.shortRow === 'exit') return edge(id, direction < 0 ? config.edges?.up : config.edges?.down);
        return at(Math.min(last, target));
      };
      return {left: column === 0 ? edge(id, config.edges?.left) : at(index - 1),
        right: column === columns - 1 || config.grid.count !== undefined && index === config.grid.count - 1 ? edge(id, config.edges?.right) : at(index + 1),
        up: vertical(-1), down: vertical(1)};
    }
    if (!config.timeGrid || current.row === undefined || !current.kind) return {};
    const row = current.row;
    const programmes = rows.get(row)?.programmes ?? [], position = positions.get(id) ?? -1;
    const channel = rows.get(row)?.channel;
    const targetTime = Math.max(config.timeGrid.start, Math.min(config.timeGrid.end - 1, memory.time ?? config.timeGrid.initialTime));
    const atTime = (at: number): CellDestination => {
      const cells = rows.get(at)?.programmes ?? [];
      const found = cells.find(member => member.cell.start! <= targetTime && member.cell.end! > targetTime)
        ?? cells.find(member => member.cell.end! > targetTime) ?? cells.at(-1);
      return self(found?.id ?? id);
    };
    const vertical = (delta: -1 | 1) => row + delta < 0 ? edge(id, config.edges?.up)
      : current.kind === 'channel' ? self(rows.get(row + delta)?.channel?.id ?? id) : atTime(row + delta);
    return {left: current.kind === 'channel' ? edge(id, config.edges?.left) : self(programmes[position - 1]?.id ?? channel?.id ?? id),
      right: current.kind === 'channel' ? self(programmes.find(member => !member.cell.boundary)?.id ?? id) : self(programmes[position + 1]?.id ?? id),
      up: vertical(-1), down: vertical(1)};
  };
}

export function cellFocusMemory(current: FocusCell | undefined, previous: FocusCell | undefined, config: CellNavigation,
  memory: Readonly<{column?: number; time?: number}>) {
  if (config.grid && current?.index !== undefined) {
    const columns = Math.max(1, Math.floor(config.grid.columns));
    const sameRow = previous?.index !== undefined && Math.floor(previous.index / columns) === Math.floor(current.index / columns);
    const lateral = sameRow && previous?.index !== current.index;
    return {...memory, column: lateral || memory.column === undefined ? current.index % columns : memory.column};
  }
  if (config.timeGrid && current?.kind === 'programme') {
    // Only a lateral programme move sets a new anchor. A wide cell reached vertically keeps it.
    const lateral = previous !== undefined && previous.row === current.row && (previous.kind === 'channel' || previous.start !== current.start);
    return {...memory, time: lateral ? current.start : memory.time ?? config.timeGrid.initialTime};
  }
  return memory;
}
