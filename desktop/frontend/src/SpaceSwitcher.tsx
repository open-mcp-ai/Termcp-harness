import { useEffect, useRef, useState } from "react";
import {
  MAX_PERSONAL_WORKSPACES,
  MAX_WORKSPACE_NAME_LENGTH,
  personalWorkspaceCount,
  workspaceNameError,
  workspaceTemplateOptions,
  type WorkspaceId,
  type WorkspaceRecord,
  type WorkspaceTemplateId,
} from "./canvas";

interface SpaceSwitcherProps {
  spaces: WorkspaceRecord[];
  activeSpaceId: WorkspaceId;
  panelCounts: Partial<Record<WorkspaceId, number>>;
  onSelect: (spaceId: WorkspaceId) => void;
  onCreate: (name: string, template: WorkspaceTemplateId) => void;
  onRename: (spaceId: WorkspaceId, name: string) => void;
  onDelete: (spaceId: WorkspaceId) => void;
}

type EditorMode = "create" | "rename" | "delete" | null;

function templateLabel(template: WorkspaceTemplateId): string {
  return workspaceTemplateOptions.find((option) => option.id === template)?.label ?? "自定义布局";
}

function spaceInitial(name: string): string {
  return Array.from(name.trim())[0]?.toLocaleUpperCase() ?? "S";
}

export default function SpaceSwitcher({
  spaces,
  activeSpaceId,
  panelCounts,
  onSelect,
  onCreate,
  onRename,
  onDelete,
}: SpaceSwitcherProps) {
  const [open, setOpen] = useState(false);
  const [editor, setEditor] = useState<EditorMode>(null);
  const [draftName, setDraftName] = useState("");
  const [template, setTemplate] = useState<WorkspaceTemplateId>("blank");
  const [error, setError] = useState<string | null>(null);
  const [targetSpaceId, setTargetSpaceId] = useState<WorkspaceId | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  const activeSpace = spaces.find((space) => space.id === activeSpaceId) ?? spaces[0];
  const personalSpaces = spaces.filter((space) => space.kind === "personal");
  const presetSpaces = spaces.filter((space) => space.kind === "preset");
  const atPersonalLimit = personalWorkspaceCount(spaces) >= MAX_PERSONAL_WORKSPACES;
  const targetSpace = targetSpaceId
    ? spaces.find((space) => space.id === targetSpaceId)
    : undefined;

  const showList = () => {
    setEditor(null);
    setError(null);
    setTargetSpaceId(null);
  };

  const close = () => {
    setOpen(false);
    showList();
  };

  const beginCreate = () => {
    setEditor("create");
    setDraftName("");
    setTemplate("blank");
    setError(null);
    setTargetSpaceId(null);
  };

  const beginRename = (space: WorkspaceRecord) => {
    setEditor("rename");
    setDraftName(space.name);
    setError(null);
    setTargetSpaceId(space.id);
  };

  const beginDelete = (space: WorkspaceRecord) => {
    setEditor("delete");
    setDraftName("");
    setError(null);
    setTargetSpaceId(space.id);
  };

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (rootRef.current?.contains(event.target as Node)) return;
      close();
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      close();
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsidePointer);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const submitName = () => {
    const nameError = workspaceNameError(
      draftName,
      spaces,
      editor === "rename" ? targetSpaceId ?? undefined : undefined,
    );
    if (nameError) {
      setError(nameError);
      return;
    }
    if (editor === "create") {
      if (atPersonalLimit) {
        setError(`最多保留 ${MAX_PERSONAL_WORKSPACES} 个自定义 Space。`);
        return;
      }
      onCreate(draftName.trim().replace(/\s+/g, " "), template);
      close();
      return;
    }
    if (editor === "rename" && targetSpaceId) {
      onRename(targetSpaceId, draftName.trim().replace(/\s+/g, " "));
      close();
    }
  };

  const submitDelete = () => {
    if (!targetSpace) return;
    onDelete(targetSpace.id);
    close();
  };

  return (
    <div className="space-switcher no-drag" ref={rootRef}>
      <button
        type="button"
        className="space-switcher-trigger"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => {
          if (open) {
            close();
          } else {
            setOpen(true);
            showList();
          }
        }}
      >
        <span className="space-switcher-kicker">Space</span>
        <span className={`space-switcher-mark ${activeSpace?.kind ?? "personal"}`} aria-hidden="true">
          {spaceInitial(activeSpace?.name ?? "Space")}
        </span>
        <span className="space-switcher-current">
          <strong>{activeSpace?.name ?? "未知 Space"}</strong>
          <small>{panelCounts[activeSpaceId] ?? 0} 个模块</small>
        </span>
        <span className={`space-switcher-chevron ${open ? "is-open" : ""}`} aria-hidden="true">⌄</span>
      </button>

      {open ? (
        <section
          className="space-popover"
          role="dialog"
          aria-modal="false"
          aria-label="切换或管理 Space"
        >
          <header className="space-popover-header">
            {editor ? (
              <button type="button" className="space-back" onClick={showList}>
                返回
              </button>
            ) : (
              <div>
                <span>Workspace navigator</span>
                <strong>我的 Spaces</strong>
              </div>
            )}
            <button type="button" className="space-popover-close" aria-label="关闭 Space 菜单" onClick={close}>
              ×
            </button>
          </header>

          {editor === null ? (
            <div className="space-popover-body">
              <section className="space-personal-group" aria-label="我的 Spaces">
                <div className="space-group-heading">
                  <span>我的 Spaces</span>
                  <span>{personalSpaces.length}</span>
                </div>
                <div className="space-list">
                  {personalSpaces.map((space) => {
                    const active = space.id === activeSpaceId;
                    return (
                      <div className={`space-list-row ${active ? "is-active" : ""}`} key={space.id}>
                        <button
                          type="button"
                          className="space-list-select"
                          aria-current={active ? "page" : undefined}
                          onClick={() => {
                            onSelect(space.id);
                            close();
                          }}
                        >
                          <span className="space-list-mark" aria-hidden="true">{spaceInitial(space.name)}</span>
                          <span className="space-list-copy">
                            <strong>{space.name}</strong>
                            <small>{panelCounts[space.id] ?? 0} 个模块 · {templateLabel(space.template)}</small>
                          </span>
                          <span className="space-list-check" aria-hidden="true">{active ? "✓" : ""}</span>
                        </button>
                        <div className="space-row-actions">
                          <button
                            type="button"
                            aria-label={`重命名 ${space.name}`}
                            title="重命名"
                            onClick={() => beginRename(space)}
                          >
                            重命名
                          </button>
                          <button
                            type="button"
                            aria-label={`删除 ${space.name}`}
                            title="删除"
                            onClick={() => beginDelete(space)}
                          >
                            删除
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
                <button
                  type="button"
                  className="space-create-entry"
                  onClick={beginCreate}
                  disabled={atPersonalLimit}
                >
                  <span aria-hidden="true">+</span>
                  <span>
                    <strong>新建 Space</strong>
                    <small>{atPersonalLimit ? "已达到本机上限" : "选择一个起点，再按你的工作方式调整"}</small>
                  </span>
                </button>
              </section>

              <details className="space-presets">
                <summary>
                  <span>场景模板</span>
                  <span>{presetSpaces.length} 个固定入口</span>
                </summary>
                <div className="space-preset-list">
                  {presetSpaces.map((space) => {
                    const active = space.id === activeSpaceId;
                    return (
                      <button
                        type="button"
                        key={space.id}
                        className={active ? "is-active" : ""}
                        onClick={() => {
                          onSelect(space.id);
                          close();
                        }}
                      >
                        <span className="space-list-copy">
                          <strong>{space.name}</strong>
                          <small>{panelCounts[space.id] ?? 0} 个模块</small>
                        </span>
                        <span aria-hidden="true">{active ? "当前" : "打开"}</span>
                      </button>
                    );
                  })}
                </div>
              </details>
            </div>
          ) : editor === "create" || editor === "rename" ? (
            <form
              className="space-editor"
              onSubmit={(event) => {
                event.preventDefault();
                submitName();
              }}
            >
              <div className="space-editor-heading">
                <span>{editor === "create" ? "New Space" : "Rename Space"}</span>
                <h3>{editor === "create" ? "创建一个自己的 Space" : `重命名“${targetSpace?.name ?? ""}”`}</h3>
                <p>
                  {editor === "create"
                    ? "名称只影响导航；模块、布局和 Session 选择都独立保存。"
                    : "正在运行的任务、终端和审批不会因重命名而改变。"}
                </p>
              </div>
              <label className="space-name-field">
                <span>Space 名称</span>
                <input
                  autoFocus
                  value={draftName}
                  maxLength={MAX_WORKSPACE_NAME_LENGTH}
                  onChange={(event) => {
                    setDraftName(event.target.value);
                    setError(null);
                  }}
                  placeholder="例如：发布值守、个人实验"
                />
                <small>{draftName.length}/{MAX_WORKSPACE_NAME_LENGTH}</small>
              </label>
              {editor === "create" ? (
                <fieldset className="space-template-picker">
                  <legend>从什么开始</legend>
                  <div>
                    {workspaceTemplateOptions.map((option) => (
                      <label className={template === option.id ? "is-selected" : ""} key={option.id}>
                        <input
                          type="radio"
                          name="space-template"
                          value={option.id}
                          checked={template === option.id}
                          onChange={() => setTemplate(option.id)}
                        />
                        <span>
                          <strong>{option.label}</strong>
                          <small>{option.description}</small>
                        </span>
                      </label>
                    ))}
                  </div>
                </fieldset>
              ) : null}
              {error ? <p className="space-form-error" role="alert">{error}</p> : null}
              <footer className="space-editor-actions">
                <button type="button" onClick={showList}>取消</button>
                <button type="submit" className="space-confirm">
                  {editor === "create" ? "创建并打开" : "保存名称"}
                </button>
              </footer>
            </form>
          ) : (
            <div className="space-delete-confirm">
              <span>Delete Space</span>
              <h3>删除“{targetSpace?.name ?? "这个 Space"}”？</h3>
              <p>
                这会移除该 Space 的布局、撤销历史和 Session 选择。正在运行的任务、终端和审批不受影响。
              </p>
              <footer className="space-editor-actions">
                <button type="button" onClick={showList}>取消</button>
                <button type="button" className="space-danger" onClick={submitDelete}>删除 Space</button>
              </footer>
            </div>
          )}
        </section>
      ) : null}
    </div>
  );
}
