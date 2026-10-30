import type {NativeHomeCustomizeController} from './customize-controller';

export type HomeDragRow = Readonly<{id: string; top: number; height: number}>;

/** The grip owns one draft pickup. Layout is captured before the row is lifted,
 * so moving the draft cannot move the gesture's crossing thresholds. */
export function createHomeRowDrag(controller: NativeHomeCustomizeController, id: string, geometry: () => readonly HomeDragRow[]) {
  let held: Readonly<{order: readonly string[]; rows: readonly HomeDragRow[]; center: number}> | undefined;
  const current = () => held && controller.isCurrent() && controller.getSnapshot().pickedId === id
    && controller.getSnapshot().pickupOrder === held.order;
  return {
    begin(): boolean {
      if (held || !controller.isCurrent() || controller.getSnapshot().pickedId) return false;
      const rows = geometry(), row = rows.find(value => value.id === id);
      if (!row || !controller.pick(id)) return false;
      held = {order: controller.getSnapshot().pickupOrder!, rows, center: row.top + row.height / 2};
      return true;
    },
    update(translationY: number): void {
      if (!held || !current() || !Number.isFinite(translationY)) return;
      const center = held.center + translationY;
      let target = 0;
      for (let index = 1; index < held.rows.length; index++) {
        const before = held.rows[index - 1], after = held.rows[index];
        const boundary = (before.top + before.height / 2 + after.top + after.height / 2) / 2;
        if (center < boundary) break;
        target = index;
      }
      let from = controller.getSnapshot().rows.findIndex(row => row.id === id);
      while (from >= 0 && from !== target) {
        const direction = from < target ? 1 : -1;
        if (!controller.move(direction)) break;
        from += direction;
      }
    },
    finish(success: boolean): void {
      if (current()) {
        if (success) controller.drop();
        else controller.cancelPickup();
      }
      held = undefined;
    },
  };
}
