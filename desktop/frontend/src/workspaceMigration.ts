import {
  normalizeLayout,
  workspaceLabels,
  type PanelLayout,
  type WorkspaceId,
} from "./canvas.ts";
import type { WorkspaceLayoutSnapshot } from "./workspaceHistory.ts";
import {
  WorkspaceRecipeError,
  createWorkspaceRecipe,
} from "./workspaceRecipe.ts";

export interface WorkspaceMigrationPreview {
  source: WorkspaceId;
  target: WorkspaceId;
  snapshot: WorkspaceLayoutSnapshot;
  replacedPanelCount: number;
  targetHadSelectedSession: boolean;
}

export class WorkspaceMigrationError extends Error {
  code: "same_workspace" | "empty_source";

  constructor(code: "same_workspace" | "empty_source", message: string) {
    super(message);
    this.name = "WorkspaceMigrationError";
    this.code = code;
  }
}

export function buildWorkspaceMigration(
  source: WorkspaceId,
  sourceLayout: PanelLayout[],
  sourceSelectedSessionId: string | undefined,
  target: WorkspaceId,
  targetLayout: PanelLayout[],
  targetSelectedSessionId: string | undefined,
): WorkspaceMigrationPreview {
  if (source === target) {
    throw new WorkspaceMigrationError("same_workspace", "源和目标 Workspace 不能相同。");
  }
  let recipe;
  try {
    recipe = createWorkspaceRecipe(source, sourceLayout, sourceSelectedSessionId);
  } catch (error) {
    if (error instanceof WorkspaceRecipeError && error.code === "empty_layout") {
      throw new WorkspaceMigrationError("empty_source", "当前 Workspace 没有可复制的面板。");
    }
    throw error;
  }
  return {
    source,
    target,
    snapshot: {
      layout: recipe.layout,
      ...(recipe.selected_session_id
        ? { selectedSessionId: recipe.selected_session_id }
        : {}),
    },
    replacedPanelCount: normalizeLayout(targetLayout).length,
    targetHadSelectedSession: Boolean(targetSelectedSessionId),
  };
}

export function migrationDescription(preview: WorkspaceMigrationPreview): string {
  const target = workspaceLabels[preview.target];
  return `将 ${preview.snapshot.layout.length} 个面板复制到“${target}”，替换其现有 ${preview.replacedPanelCount} 个面板。`;
}
