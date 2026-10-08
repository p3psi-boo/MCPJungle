import type {
  DashboardCreateToolGroupInput,
  DashboardDiagnosticsResponse,
  DashboardInstallSkillsInput,
  DashboardInstallSkillsResponse,
  DashboardOAuthSessionResponse,
  DashboardOverviewResponse,
  DashboardPromptsResponse,
  DashboardRegisterServerInput,
  DashboardRegisterServerResponse,
  DashboardResourcesResponse,
  DashboardServersResponse,
  DashboardSkill,
  DashboardSkillDetail,
  DashboardSkillPreviewResponse,
  DashboardSkillsResponse,
  DashboardToolGroupsResponse,
  DashboardToolsResponse,
} from "./types";

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: "application/json",
      ...(init?.headers ?? {}),
    },
  });
  if (!response.ok) {
    let message = `Request failed: ${response.status}`;
    try {
      const payload = (await response.json()) as { error?: string };
      if (payload.error) {
        message = payload.error;
      }
    } catch {
      // keep the fallback message
    }
    throw new Error(message);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export const api = {
  overview: () => requestJSON<DashboardOverviewResponse>("/api/dashboard/overview"),
  servers: () => requestJSON<DashboardServersResponse>("/api/dashboard/servers"),
  tools: () => requestJSON<DashboardToolsResponse>("/api/dashboard/tools"),
  toolGroups: () => requestJSON<DashboardToolGroupsResponse>("/api/dashboard/tool-groups"),
  prompts: () => requestJSON<DashboardPromptsResponse>("/api/dashboard/prompts"),
  resources: () => requestJSON<DashboardResourcesResponse>("/api/dashboard/resources"),
  diagnostics: () => requestJSON<DashboardDiagnosticsResponse>("/api/dashboard/diagnostics"),
  registerServer: (body: DashboardRegisterServerInput) =>
    requestJSON<DashboardRegisterServerResponse>("/api/dashboard/servers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  getOAuthSession: (sessionID: string) =>
    requestJSON<DashboardOAuthSessionResponse>(`/api/dashboard/oauth/session/${encodeURIComponent(sessionID)}`),
  createToolGroup: (body: DashboardCreateToolGroupInput) =>
    requestJSON("/api/dashboard/tool-groups", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  deleteToolGroup: (name: string) =>
    requestJSON(`/api/dashboard/tool-groups/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),
  deleteServer: (name: string) =>
    requestJSON(`/api/dashboard/servers/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),
  setServerEnabled: (name: string, enabled: boolean) =>
    requestJSON(`/api/dashboard/servers/${encodeURIComponent(name)}/enabled`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
  setToolEnabled: (name: string, enabled: boolean) =>
    requestJSON(`/api/dashboard/tools/${encodeURIComponent(name)}/enabled`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
  setPromptEnabled: (name: string, enabled: boolean) =>
    requestJSON(`/api/dashboard/prompts/${encodeURIComponent(name)}/enabled`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    }),
  skills: () => requestJSON<DashboardSkillsResponse>("/api/dashboard/skills"),
  skill: (name: string) =>
    requestJSON<DashboardSkillDetail>(`/api/dashboard/skills/${encodeURIComponent(name)}`),
  previewSkills: (source: string) =>
    requestJSON<DashboardSkillPreviewResponse>("/api/dashboard/skills/preview", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source }),
    }),
  installSkills: (body: DashboardInstallSkillsInput) =>
    requestJSON<DashboardInstallSkillsResponse>("/api/dashboard/skills", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  updateSkill: (name: string) =>
    requestJSON<DashboardSkill>(`/api/dashboard/skills/${encodeURIComponent(name)}/update`, {
      method: "POST",
    }),
  removeSkill: (name: string) =>
    requestJSON(`/api/dashboard/skills/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),
  reloadSkills: () =>
    requestJSON<{ skill_count: number }>("/api/dashboard/skills/reload", {
      method: "POST",
    }),
};
