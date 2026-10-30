import {
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  useRef,
  useState,
} from "react";
import {
  CANVAS_COLUMNS,
  CANVAS_ROWS,
  clamp,
  layoutHasOverlap,
  moduleDefinitions,
  type ModuleId,
  type PanelLayout,
} from "./canvas";

const ROW_HEIGHT = 44;
const GAP = 8;

interface CanvasGridProps {
  panels: PanelLayout[];
  onChange: (panels: PanelLayout[]) => void;
  onClose: (moduleId: ModuleId) => void;
  onMaximize: (moduleId: ModuleId) => void;
  renderPanel: (panel: PanelLayout) => ReactNode;
}

interface DragState {
  mode: "move" | "resize";
  moduleId: ModuleId;
  pointerId: number;
  startX: number;
  startY: number;
  cellWidth: number;
  original: PanelLayout[];
  latest: PanelLayout[];
}

export default function CanvasGrid({
  panels,
  onChange,
  onClose,
  onMaximize,
  renderPanel,
}: CanvasGridProps) {
  const boardRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<DragState | null>(null);
  const [dragging, setDragging] = useState<ModuleId | null>(null);

  const startPointer = (
    event: ReactPointerEvent<HTMLElement>,
    panel: PanelLayout,
    mode: "move" | "resize",
  ) => {
    if (event.button !== 0 || panel.maximized || !boardRef.current) return;
    const rect = boardRef.current.getBoundingClientRect();
    const cellWidth = (rect.width - GAP * (CANVAS_COLUMNS - 1)) / CANVAS_COLUMNS;
    dragRef.current = {
      mode,
      moduleId: panel.moduleId,
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      cellWidth,
      original: panels,
      latest: panels,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    setDragging(panel.moduleId);
    event.preventDefault();
  };

  const movePointer = (event: ReactPointerEvent<HTMLElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const panel = drag.original.find((candidate) => candidate.moduleId === drag.moduleId);
    if (!panel) return;
    const cellHeight = ROW_HEIGHT + GAP;
    const dx = Math.round((event.clientX - drag.startX) / (drag.cellWidth + GAP));
    const dy = Math.round((event.clientY - drag.startY) / cellHeight);
    const definition = moduleDefinitions.find((candidate) => candidate.id === drag.moduleId)!;

    const next = drag.original.map((candidate) => {
      if (candidate.moduleId !== drag.moduleId) return candidate;
      if (drag.mode === "move") {
        return {
          ...candidate,
          x: clamp(panel.x + dx, 0, CANVAS_COLUMNS - panel.w),
          y: clamp(panel.y + dy, 0, CANVAS_ROWS - panel.h),
        };
      }
      const w = clamp(panel.w + dx, definition.minW, CANVAS_COLUMNS - panel.x);
      const h = clamp(panel.h + dy, definition.minH, CANVAS_ROWS - panel.y);
      return { ...candidate, w, h };
    });
    drag.latest = next;
    onChange(next);
  };

  const endPointer = (event: ReactPointerEvent<HTMLElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(null);
    if (layoutHasOverlap(drag.latest)) onChange(drag.original);
  };

  const moveWithKeyboard = (event: ReactKeyboardEvent<HTMLElement>, panel: PanelLayout) => {
    const deltas: Record<string, [number, number]> = {
      ArrowLeft: [-1, 0],
      ArrowRight: [1, 0],
      ArrowUp: [0, -1],
      ArrowDown: [0, 1],
    };
    const delta = deltas[event.key];
    if (!delta || panel.maximized) return;
    event.preventDefault();
    const candidate = {
      ...panel,
      x: clamp(panel.x + delta[0], 0, CANVAS_COLUMNS - panel.w),
      y: clamp(panel.y + delta[1], 0, CANVAS_ROWS - panel.h),
    };
    const next = panels.map((item) => item.moduleId === panel.moduleId ? candidate : item);
    if (!layoutHasOverlap(next)) onChange(next);
  };

  return (
    <div className="canvas-board" ref={boardRef}>
      {panels.map((panel) => {
        const definition = moduleDefinitions.find((candidate) => candidate.id === panel.moduleId);
        if (!definition) return null;
        const placement = panel.maximized
          ? { gridColumn: `1 / span ${CANVAS_COLUMNS}`, gridRow: `1 / span ${CANVAS_ROWS}` }
          : {
              gridColumn: `${panel.x + 1} / span ${panel.w}`,
              gridRow: `${panel.y + 1} / span ${panel.h}`,
            };
        return (
          <section
            className={`canvas-panel ${dragging === panel.moduleId ? "is-dragging" : ""} ${panel.maximized ? "is-maximized" : ""}`}
            key={panel.moduleId}
            style={placement}
            aria-label={definition.title}
          >
            <header className="canvas-panel-header">
              <button
                type="button"
                className="canvas-panel-title"
                title="拖动面板；方向键可移动"
                onPointerDown={(event) => startPointer(event, panel, "move")}
                onPointerMove={movePointer}
                onPointerUp={endPointer}
                onPointerCancel={endPointer}
                onKeyDown={(event) => moveWithKeyboard(event, panel)}
              >
                <span className="canvas-panel-grip" aria-hidden="true">⠿</span>
                <strong>{definition.title}</strong>
              </button>
              <div className="canvas-panel-actions">
                <button type="button" aria-label={`${panel.maximized ? "还原" : "最大化"} ${definition.title}`} onClick={() => onMaximize(panel.moduleId)}>
                  {panel.maximized ? "▣" : "□"}
                </button>
                <button type="button" aria-label={`关闭 ${definition.title}`} onClick={() => onClose(panel.moduleId)}>
                  ×
                </button>
              </div>
            </header>
            <div className="canvas-panel-content">{renderPanel(panel)}</div>
            {!panel.maximized ? (
              <button
                type="button"
                className="canvas-panel-resize"
                aria-label={`调整 ${definition.title} 大小`}
                onPointerDown={(event) => startPointer(event, panel, "resize")}
                onPointerMove={movePointer}
                onPointerUp={endPointer}
                onPointerCancel={endPointer}
              />
            ) : null}
          </section>
        );
      })}
    </div>
  );
}
