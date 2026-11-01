import assert from "node:assert/strict";
import test from "node:test";

import {
  CANVAS_COLUMNS,
  CANVAS_ROWS,
  canvasRowHeight,
  defaultWorkspaceLayout,
  normalizeLayout,
} from "../src/canvas.ts";

function maximizedLayout() {
  const template = defaultWorkspaceLayout("daily");
  const first = template[0];
  return template.map((panel, index) => index === 0
    ? {
        ...panel,
        maximized: true,
        restore: { x: panel.x, y: panel.y, w: panel.w, h: panel.h },
        x: 0,
        y: 0,
        w: CANVAS_COLUMNS,
        h: CANVAS_ROWS,
      }
    : panel);
}

test("runtime normalization preserves a valid maximized panel", () => {
  const input = maximizedLayout();
  const normalized = normalizeLayout(input);

  assert.equal(normalized[0].maximized, true);
  assert.deepEqual(normalized[0].restore, input[0].restore);
  assert.equal(normalized[0].w, CANVAS_COLUMNS);
  assert.equal(normalized[0].h, CANVAS_ROWS);
});

test("persisted normalization collapses maximized panels to restore geometry", () => {
  const input = maximizedLayout();
  const expected = input[0].restore;
  const normalized = normalizeLayout(input, { collapseMaximized: true });

  assert.equal(normalized[0].maximized, false);
  assert.equal(normalized[0].restore, undefined);
  assert.deepEqual(
    {
      x: normalized[0].x,
      y: normalized[0].y,
      w: normalized[0].w,
      h: normalized[0].h,
    },
    expected,
  );
});

test("malformed maximized state is discarded during runtime normalization", () => {
  const template = defaultWorkspaceLayout("daily");
  const normalized = normalizeLayout(template.map((panel, index) => index === 0
    ? { ...panel, maximized: true }
    : panel));

  assert.equal(normalized[0].maximized, false);
  assert.equal(normalized[0].restore, undefined);
});

test("canvas rows fill the available board height without a fixed 616px floor", () => {
  assert.equal(canvasRowHeight(616), 44);
  assert.ok(canvasRowHeight(520) < canvasRowHeight(616));
  assert.ok(canvasRowHeight(720) > canvasRowHeight(616));
  assert.equal(canvasRowHeight(0), 1);
});

test("normalization isolates malformed panels and strips unknown fields", () => {
  const template = defaultWorkspaceLayout("daily");
  const normalized = normalizeLayout([
    null,
    { ...template[0], unsupported: "must not persist" },
    { ...template[1], x: Number.NaN },
  ]);

  assert.equal(normalized.length, 2);
  assert.equal("unsupported" in normalized[0], false);
  assert.equal(normalized[1].x, 0);
});
