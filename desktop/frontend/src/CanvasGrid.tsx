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
  panelMinimumSize,
  panelTabs,
  type ModuleId,
  type PanelLayout,
} from "./canvas";

const ROW_HEIGHT = 44;
const GAP = 8;

type DropZone = "tab" | "left" | "right" | "top" | "bottom";

interface DropPreview {
  targetId: string;
  zone: DropZone;
}

interface CanvasGridProps {
  panels: PanelLayout[];
  onChange: (panels: PanelLayout[]) => void;
  onClose: (panelId: string, moduleId: ModuleId) => void;
  onMaximize: (panelId: string) => void;
  renderPanel: (panel: PanelLayout) => ReactNode;
  renderContextBar?: (panel: PanelLayout, moduleId: ModuleId) => ReactNode;
}

interface DragState {
  mode: "move" | "resize";
  panelId: string;
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
  renderContextBar,
}: CanvasGridProps) {
  const boardRef = useRef<HTMLDivElement>(null);
  const dragRef = useRef<DragState | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [dropPreview, setDropPreview] = useState<DropPreview | null>(null);

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
      panelId: panel.id,
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      cellWidth,
      original: panels,
      latest: panels,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    setDragging(panel.id);
    event.preventDefault();
  };

  const dropTargetAt = (clientX: number, clientY: number, sourceId: string): DropPreview | null => {
    if (!boardRef.current) return null;
    const rect = boardRef.current.getBoundingClientRect();
    const cellWidth = (rect.width - GAP * (CANVAS_COLUMNS - 1)) / CANVAS_COLUMNS;
    const column = Math.floor((clientX - rect.left + GAP / 2) / (cellWidth + GAP));
    const row = Math.floor((clientY - rect.top + GAP / 2) / (ROW_HEIGHT + GAP));
    if (column < 0 || column >= CANVAS_COLUMNS || row < 0 || row >= CANVAS_ROWS) return null;
    const target = panels.find((panel) =>
      panel.id !== sourceId &&
      column >= panel.x && column < panel.x + panel.w &&
      row >= panel.y && row < panel.y + panel.h,
    );
    if (!target) return null;
    if (row === target.y) return { targetId: target.id, zone: "tab" };
    const relativeX = (column - target.x + 0.5) / target.w;
    const relativeY = (row - target.y + 0.5) / target.h;
    if (relativeX < 0.25) return { targetId: target.id, zone: "left" };
    if (relativeX > 0.75) return { targetId: target.id, zone: "right" };
    if (relativeY < 0.3) return { targetId: target.id, zone: "top" };
    if (relativeY > 0.7) return { targetId: target.id, zone: "bottom" };
    return { targetId: target.id, zone: "tab" };
  };

  const movePointer = (event: ReactPointerEvent<HTMLElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const panel = drag.original.find((candidate) => candidate.id === drag.panelId);
    if (!panel) return;
    const cellHeight = ROW_HEIGHT + GAP;
    const dx = Math.round((event.clientX - drag.startX) / (drag.cellWidth + GAP));
    const dy = Math.round((event.clientY - drag.startY) / cellHeight);
    const minimum = panelMinimumSize(panel);

    const next = drag.original.map((candidate) => {
      if (candidate.id !== drag.panelId) return candidate;
      if (drag.mode === "move") {
        return {
          ...candidate,
          x: clamp(panel.x + dx, 0, CANVAS_COLUMNS - panel.w),
          y: clamp(panel.y + dy, 0, CANVAS_ROWS - panel.h),
        };
      }
      const w = clamp(panel.w + dx, minimum.w, CANVAS_COLUMNS - panel.x);
      const h = clamp(panel.h + dy, minimum.h, CANVAS_ROWS - panel.y);
      return { ...candidate, w, h };
    });
    drag.latest = next;
    onChange(next);
    setDropPreview(drag.mode === "move" ? dropTargetAt(event.clientX, event.clientY, drag.panelId) : null);
  };

  const mergePanels = (source: PanelLayout, target: PanelLayout, original: PanelLayout[]): PanelLayout[] => {
    const tabs = Array.from(new Set([...panelTabs(target), ...panelTabs(source)]));
    return original
      .filter((panel) => panel.id !== source.id)
      .map((panel) =>
        panel.id === target.id
          ? {
              ...panel,
              moduleId: source.moduleId,
              tabs,
              contextByModule: {
                ...panel.contextByModule,
                ...source.contextByModule,
              },
            }
          : panel,
      );
  };

  const splitPanels = (
    source: PanelLayout,
    target: PanelLayout,
    zone: Exclude<DropZone, "tab">,
    original: PanelLayout[],
  ): PanelLayout[] | null => {
    const horizontal = zone === "left" || zone === "right";
    const sourceMinimum = panelMinimumSize(source);
    const targetMinimum = panelMinimumSize(target);
    if (horizontal && target.w < sourceMinimum.w + targetMinimum.w) return null;
    if (!horizontal && target.h < sourceMinimum.h + targetMinimum.h) return null;

    let sourceNext: PanelLayout;
    let targetNext: PanelLayout;
    if (horizontal) {
      const firstWidth = Math.floor(target.w / 2);
      const secondWidth = target.w - firstWidth;
      const sourceUsesFirst = zone === "left";
      sourceNext = {
        ...source,
        x: target.x + (sourceUsesFirst ? 0 : firstWidth),
        y: target.y,
        w: sourceUsesFirst ? firstWidth : secondWidth,
        h: target.h,
      };
      targetNext = {
        ...target,
        x: target.x + (sourceUsesFirst ? firstWidth : 0),
        y: target.y,
        w: sourceUsesFirst ? secondWidth : firstWidth,
        h: target.h,
      };
    } else {
      const firstHeight = Math.floor(target.h / 2);
      const secondHeight = target.h - firstHeight;
      const sourceUsesFirst = zone === "top";
      sourceNext = {
        ...source,
        x: target.x,
        y: target.y + (sourceUsesFirst ? 0 : firstHeight),
        w: target.w,
        h: sourceUsesFirst ? firstHeight : secondHeight,
      };
      targetNext = {
        ...target,
        x: target.x,
        y: target.y + (sourceUsesFirst ? firstHeight : 0),
        w: target.w,
        h: sourceUsesFirst ? secondHeight : firstHeight,
      };
    }
    return original.map((panel) =>
      panel.id === source.id ? sourceNext : panel.id === target.id ? targetNext : panel,
    );
  };

  const endPointer = (event: ReactPointerEvent<HTMLElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(null);
    const preview = dropPreview;
    setDropPreview(null);
    if (drag.mode === "move" && preview && preview.targetId !== drag.panelId) {
      const source = drag.original.find((panel) => panel.id === drag.panelId);
      const target = drag.original.find((panel) => panel.id === preview.targetId);
      if (source && target) {
        if (preview.zone === "tab") {
          onChange(mergePanels(source, target, drag.original));
        } else {
          const split = splitPanels(source, target, preview.zone, drag.original);
          onChange(split ?? mergePanels(source, target, drag.original));
        }
        return;
      }
    }
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
    const next = panels.map((item) => item.id === panel.id ? candidate : item);
    if (!layoutHasOverlap(next)) onChange(next);
  };

  const activateTab = (panel: PanelLayout, moduleId: ModuleId) => {
    onChange(panels.map((candidate) =>
      candidate.id === panel.id
        ? { ...candidate, moduleId, tabs: panelTabs(candidate) }
        : candidate,
    ));
  };

  return (
    <div className="canvas-board" ref={boardRef}>
      {panels.map((panel) => {
        const definition = moduleDefinitions.find((candidate) => candidate.id === panel.moduleId);
        if (!definition) return null;
        const tabs = panelTabs(panel);
        const contextBar = renderContextBar?.(panel, panel.moduleId);
        const placement = panel.maximized
          ? { gridColumn: `1 / span ${CANVAS_COLUMNS}`, gridRow: `1 / span ${CANVAS_ROWS}` }
          : {
              gridColumn: `${panel.x + 1} / span ${panel.w}`,
              gridRow: `${panel.y + 1} / span ${panel.h}`,
            };
        return (
          <section
            className={`canvas-panel ${contextBar ? "has-context" : ""} ${dragging === panel.id ? "is-dragging" : ""} ${panel.maximized ? "is-maximized" : ""}`}
            key={panel.id}
            data-panel-id={panel.id}
            style={placement}
            aria-label={definition.title}
          >
            <header className="canvas-panel-header">
              <button
                type="button"
                className="canvas-panel-grip-button"
                title="拖动面板；拖到其他标题合并，拖到边缘分栏"
                aria-label={`拖动 ${definition.title}`}
                onPointerDown={(event) => startPointer(event, panel, "move")}
                onPointerMove={movePointer}
                onPointerUp={endPointer}
                onPointerCancel={endPointer}
                onKeyDown={(event) => moveWithKeyboard(event, panel)}
              >
                ⠿
              </button>
              <div className="canvas-panel-tabs" role="tablist" aria-label={`${definition.title} 面板标签`}>
                {tabs.map((moduleId) => {
                  const tabDefinition = moduleDefinitions.find((candidate) => candidate.id === moduleId);
                  if (!tabDefinition) return null;
                  return (
                    <button
                      type="button"
                      role="tab"
                      aria-selected={moduleId === panel.moduleId}
                      className={moduleId === panel.moduleId ? "active" : ""}
                      key={moduleId}
                      onClick={() => activateTab(panel, moduleId)}
                    >
                      {tabDefinition.title}
                    </button>
                  );
                })}
              </div>
              <div className="canvas-panel-actions">
                <button type="button" aria-label={`${panel.maximized ? "还原" : "最大化"} ${definition.title}`} onClick={() => onMaximize(panel.id)}>
                  {panel.maximized ? "▣" : "□"}
                </button>
                <button
                  type="button"
                  aria-label={`关闭 ${definition.title} 视图；不会停止任务`}
                  title="关闭视图；不会停止任务"
                  onClick={() => onClose(panel.id, panel.moduleId)}
                >
                  ×
                </button>
              </div>
            </header>
            {contextBar ? <div className="canvas-panel-context">{contextBar}</div> : null}
            <div className="canvas-panel-content">{renderPanel(panel)}</div>
            {dropPreview?.targetId === panel.id ? (
              <div className={`canvas-drop-preview ${dropPreview.zone}`} aria-hidden="true" />
            ) : null}
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
