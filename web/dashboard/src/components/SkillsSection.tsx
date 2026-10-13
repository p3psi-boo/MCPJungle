import { Fragment, useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import type {
  DashboardSkill,
  DashboardSkillDetail,
  DashboardSkillOrigin,
  DashboardSkillPreviewResponse,
  DashboardSkillsResponse,
} from "@/lib/types";
import { ChevronIcon } from "./ChevronIcon";
import { CopyButton } from "./CopyButton";
import { EmptyStateCard } from "./EmptyStateCard";
import { SectionCard } from "./SectionCard";
import { StatusBadge } from "./StatusBadge";

export interface SkillsFeedback {
  tone: "success" | "error";
  message: string;
}

const exampleSources = ["anthropics/skills", "https://github.com/anthropics/skills/tree/main/skills/pdf"];
const skillNamePattern = /^[a-z0-9]+(-[a-z0-9]+)*$/;

type AddTab = "create" | "github";
type Dialog = { kind: "add"; tab: AddTab } | { kind: "edit"; name: string } | null;

interface SkillWriteForm {
  name: string;
  description: string;
  license: string;
  compatibility: string;
  instructions: string;
}

const emptyWriteForm: SkillWriteForm = {
  name: "",
  description: "",
  license: "",
  compatibility: "",
  instructions: "",
};

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Request failed";
}

function originLabel(origin: DashboardSkillOrigin) {
  let label = origin.repo;
  if (origin.path) {
    label += `/${origin.path}`;
  }
  if (origin.ref) {
    label += `@${origin.ref}`;
  }
  return label;
}

function originURL(origin: DashboardSkillOrigin) {
  const ref = origin.ref || "HEAD";
  return `https://github.com/${origin.repo}/tree/${ref}${origin.path ? `/${origin.path}` : ""}`;
}

function writePayload(form: SkillWriteForm) {
  return {
    description: form.description.trim(),
    license: form.license.trim(),
    compatibility: form.compatibility.trim(),
    instructions: form.instructions,
  };
}

function validateWriteForm(form: SkillWriteForm, requireName: boolean) {
  const name = form.name.trim();
  if (requireName) {
    if (!name) {
      return "Skill name is required.";
    }
    if (name.length > 64 || !skillNamePattern.test(name)) {
      return "Name must be at most 64 lowercase letters, digits and single hyphens.";
    }
  }
  if (!form.description.trim()) {
    return "Description is required.";
  }
  if (form.description.trim().length > 1024) {
    return "Description must be at most 1024 characters.";
  }
  return "";
}

function SkillWriteFields({
  form,
  nameReadOnly,
  onChange,
}: {
  form: SkillWriteForm;
  nameReadOnly: boolean;
  onChange: (patch: Partial<SkillWriteForm>) => void;
}) {
  return (
    <>
      <label className="form-field">
        <span>Name</span>
        <input
          autoFocus={!nameReadOnly}
          className="table-filter form-input"
          disabled={nameReadOnly}
          onChange={(event) => onChange({ name: event.target.value })}
          placeholder="code-review"
          value={form.name}
        />
        <span className="form-hint">Lowercase letters, digits and hyphens. This becomes the prompt name skills__{form.name.trim() || "name"}.</span>
      </label>
      <label className="form-field">
        <span>Description</span>
        <textarea
          className="table-filter form-input form-textarea-description"
          onChange={(event) => onChange({ description: event.target.value })}
          placeholder="Review pull requests. Use when asked to look at a diff."
          rows={4}
          value={form.description}
        />
        <span className="form-hint">Shown to the agent so it can decide when to load this skill. At most 1024 characters.</span>
      </label>
      <div className="form-grid">
        <label className="form-field">
          <span>License (optional)</span>
          <input
            className="table-filter form-input"
            onChange={(event) => onChange({ license: event.target.value })}
            placeholder="Optional, eg MIT"
            value={form.license}
          />
        </label>
        <label className="form-field">
          <span>Compatibility (optional)</span>
          <input
            className="table-filter form-input"
            onChange={(event) => onChange({ compatibility: event.target.value })}
            placeholder="Claude, Cursor"
            value={form.compatibility}
          />
        </label>
      </div>
      <label className="form-field">
        <span>Instructions</span>
        <textarea
          className="table-filter form-input form-textarea form-textarea-tall"
          onChange={(event) => onChange({ instructions: event.target.value })}
          placeholder={"# Code review\n\nStart with the diff, then check tests and edge cases."}
          value={form.instructions}
        />
        <span className="form-hint">Markdown body of SKILL.md. Frontmatter is generated from the fields above.</span>
      </label>
    </>
  );
}

export function SkillsSection({ onFeedback }: { onFeedback: (feedback: SkillsFeedback | null) => void }) {
  const [data, setData] = useState<DashboardSkillsResponse | null>(null);
  const [loadError, setLoadError] = useState("");
  const [filter, setFilter] = useState("");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [details, setDetails] = useState<Record<string, DashboardSkillDetail>>({});
  const [busy, setBusy] = useState<Record<string, boolean>>({});

  const [dialog, setDialog] = useState<Dialog>(null);
  const [writeForm, setWriteForm] = useState<SkillWriteForm>(emptyWriteForm);
  const [writeError, setWriteError] = useState("");
  const [editOrigin, setEditOrigin] = useState<DashboardSkillOrigin | null>(null);

  const [source, setSource] = useState("");
  const [preview, setPreview] = useState<DashboardSkillPreviewResponse | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [candidateFilter, setCandidateFilter] = useState("");
  const [overwrite, setOverwrite] = useState(false);
  const [addError, setAddError] = useState("");

  async function load() {
    try {
      setData(await api.skills());
      setDetails({});
      setLoadError("");
    } catch (error) {
      setLoadError(errorMessage(error));
    }
  }

  useEffect(() => {
    void load();
  }, []);

  function setBusyKey(key: string, value: boolean) {
    setBusy((current) => {
      const next = { ...current };
      if (value) {
        next[key] = true;
      } else {
        delete next[key];
      }
      return next;
    });
  }

  async function run(key: string, action: () => Promise<string>) {
    onFeedback(null);
    setBusyKey(key, true);
    try {
      const message = await action();
      await load();
      onFeedback({ tone: "success", message });
    } catch (error) {
      onFeedback({ tone: "error", message: errorMessage(error) });
    } finally {
      setBusyKey(key, false);
    }
  }

  async function loadDetail(name: string) {
    if (details[name]) {
      return details[name];
    }
    const detail = await api.skill(name);
    setDetails((current) => ({ ...current, [name]: detail }));
    return detail;
  }

  async function toggleExpanded(skill: DashboardSkill) {
    if (expanded === skill.name) {
      setExpanded(null);
      return;
    }
    setExpanded(skill.name);
    if (!details[skill.name]) {
      try {
        await loadDetail(skill.name);
      } catch (error) {
        onFeedback({ tone: "error", message: errorMessage(error) });
      }
    }
  }

  function removeSkill(skill: DashboardSkill) {
    if (!window.confirm(`Remove skill "${skill.name}"? MCP clients will no longer be able to use it.`)) {
      return;
    }
    void run(`remove:${skill.name}`, async () => {
      await api.removeSkill(skill.name);
      if (expanded === skill.name) {
        setExpanded(null);
      }
      return `${skill.name} removed.`;
    });
  }

  function resetAddState() {
    setSource("");
    setPreview(null);
    setSelected([]);
    setCandidateFilter("");
    setOverwrite(false);
    setAddError("");
    setWriteForm(emptyWriteForm);
    setWriteError("");
    setEditOrigin(null);
  }

  function openAdd(tab: AddTab = "create") {
    resetAddState();
    setDialog({ kind: "add", tab });
  }

  function closeDialog() {
    setDialog(null);
    resetAddState();
  }

  function patchWriteForm(patch: Partial<SkillWriteForm>) {
    setWriteForm((current) => ({ ...current, ...patch }));
  }

  async function openEdit(skill: DashboardSkill) {
    onFeedback(null);
    setBusyKey(`edit-load:${skill.name}`, true);
    try {
      const detail = await loadDetail(skill.name);
      setWriteForm({
        name: skill.name,
        description: skill.description,
        license: skill.license ?? "",
        compatibility: skill.compatibility ?? "",
        instructions: detail.instructions,
      });
      setWriteError("");
      setEditOrigin(skill.origin ?? null);
      setDialog({ kind: "edit", name: skill.name });
    } catch (error) {
      onFeedback({ tone: "error", message: errorMessage(error) });
    } finally {
      setBusyKey(`edit-load:${skill.name}`, false);
    }
  }

  async function submitCreate() {
    const validationError = validateWriteForm(writeForm, true);
    if (validationError) {
      setWriteError(validationError);
      return;
    }
    setWriteError("");
    setBusyKey("create", true);
    onFeedback(null);
    try {
      const result = await api.createSkill({ name: writeForm.name.trim(), ...writePayload(writeForm) });
      await load();
      setDetails((current) => ({ ...current, [result.name]: result }));
      setExpanded(result.name);
      onFeedback({ tone: "success", message: `Created skill ${result.name}.` });
      closeDialog();
    } catch (error) {
      setWriteError(errorMessage(error));
    } finally {
      setBusyKey("create", false);
    }
  }

  async function submitEdit() {
    if (!dialog || dialog.kind !== "edit") {
      return;
    }
    const validationError = validateWriteForm(writeForm, false);
    if (validationError) {
      setWriteError(validationError);
      return;
    }
    setWriteError("");
    setBusyKey("edit", true);
    onFeedback(null);
    try {
      const result = await api.editSkill(dialog.name, writePayload(writeForm));
      await load();
      setDetails((current) => ({ ...current, [result.name]: result }));
      setExpanded(result.name);
      onFeedback({ tone: "success", message: `Saved ${result.name}.` });
      closeDialog();
    } catch (error) {
      setWriteError(errorMessage(error));
    } finally {
      setBusyKey("edit", false);
    }
  }

  async function findSkills() {
    const value = source.trim();
    if (!value) {
      setAddError("Enter a GitHub repository, eg- anthropics/skills.");
      return;
    }
    setAddError("");
    setPreview(null);
    setSelected([]);
    setBusyKey("preview", true);
    try {
      const result = await api.previewSkills(value);
      setPreview(result);
      const installable = result.skills.filter((candidate) => !candidate.conflict && !candidate.installed);
      if (result.skills.length === 1 && installable.length === 1) {
        setSelected([installable[0].name]);
      }
    } catch (error) {
      setAddError(errorMessage(error));
    } finally {
      setBusyKey("preview", false);
    }
  }

  async function installSelected() {
    if (!preview || selected.length === 0) {
      return;
    }
    setAddError("");
    setBusyKey("install", true);
    onFeedback(null);
    try {
      const result = await api.installSkills({ source: preview.source, skills: selected, overwrite });
      await load();
      const names = result.installed.map((skill) => skill.name).join(", ");
      onFeedback({ tone: "success", message: `Installed ${result.installed.length} skill(s): ${names}.` });
      closeDialog();
    } catch (error) {
      setAddError(errorMessage(error));
    } finally {
      setBusyKey("install", false);
    }
  }

  function toggleCandidate(name: string) {
    setSelected((current) => (current.includes(name) ? current.filter((n) => n !== name) : [...current, name]));
  }

  const filteredSkills = useMemo(() => {
    const skills = data?.skills ?? [];
    const term = filter.trim().toLowerCase();
    if (!term) {
      return skills;
    }
    return skills.filter(
      (skill) => skill.name.toLowerCase().includes(term) || skill.description.toLowerCase().includes(term),
    );
  }, [data?.skills, filter]);

  const filteredCandidates = useMemo(() => {
    const candidates = preview?.skills ?? [];
    const term = candidateFilter.trim().toLowerCase();
    if (!term) {
      return candidates;
    }
    return candidates.filter(
      (candidate) =>
        candidate.name.toLowerCase().includes(term) || candidate.description.toLowerCase().includes(term),
    );
  }, [preview?.skills, candidateFilter]);

  const selectableCandidates = (preview?.skills ?? []).filter((candidate) => !candidate.conflict);
  const selectedNeedsOverwrite = (preview?.skills ?? []).some(
    (candidate) => candidate.installed && selected.includes(candidate.name),
  );
  const addTab = dialog?.kind === "add" ? dialog.tab : "create";

  if (loadError && !data) {
    return (
      <section className="loading-screen panel error-screen">
        <h2>Skills unavailable</h2>
        <code>{loadError}</code>
      </section>
    );
  }
  if (!data) {
    return (
      <section className="loading-screen panel">
        <h2>Loading skills</h2>
      </section>
    );
  }
  if (!data.enabled) {
    return (
      <EmptyStateCard
        emptyState={{
          title: "Skills are not enabled",
          description:
            "Restart MCPJungle with a skills directory to serve Agent Skills to your MCP clients and install new ones from this page.",
          commands: ["mcpjungle start --skills-install-dir ./skills"],
        }}
      />
    );
  }

  return (
    <>
      <SectionCard
        title="Skills"
        subtitle="Agent Skills served through the skills__* MCP tools and prompts"
        action={
          <div className="toolbar-cluster">
            <input
              className="table-filter compact-filter"
              onChange={(event) => setFilter(event.target.value)}
              placeholder="Search skills"
              value={filter}
            />
            <button
              className="secondary-action"
              disabled={busy.reload}
              onClick={() =>
                void run("reload", async () => {
                  const result = await api.reloadSkills();
                  return `Reloaded skills from disk, ${result.skill_count} available.`;
                })
              }
              title="Rediscover skills added or removed on disk"
              type="button"
            >
              {busy.reload ? "Reloading..." : "Reload"}
            </button>
            <button
              className="primary-action"
              disabled={!data.can_install}
              onClick={() => openAdd("create")}
              title={data.can_install ? "Create a skill or install from GitHub" : "No skills install directory is configured"}
              type="button"
            >
              + Add Skill
            </button>
          </div>
        }
      >
        {data.skills.length === 0 ? (
          <EmptyStateCard
            emptyState={{
              title: "No skills yet",
              description:
                "Click + Add Skill to create one from scratch, or install skills from a GitHub repository.",
            }}
          />
        ) : (
          <div className="tools-table-wrap">
            <table className="data-table compact-table prompts-table skills-table">
              <thead>
                <tr>
                  <th aria-hidden="true" className="expand-column"></th>
                  <th>Skill</th>
                  <th>Description</th>
                  <th>Source</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {filteredSkills.map((skill) => {
                  const isExpanded = expanded === skill.name;
                  const detail = details[skill.name];
                  return (
                    <Fragment key={skill.name}>
                      <tr
                        aria-expanded={isExpanded}
                        className={`${isExpanded ? "is-selected" : ""} tool-summary-row`}
                        onClick={() => void toggleExpanded(skill)}
                      >
                        <td className="expand-column">
                          <ChevronIcon expanded={isExpanded} />
                        </td>
                        <td>
                          <div className="table-primary">{skill.name}</div>
                          <code className="identifier-code" title={skill.prompt_name}>
                            {skill.prompt_name}
                          </code>
                        </td>
                        <td>
                          <div className="clamped-description" title={skill.description}>
                            {skill.description}
                          </div>
                        </td>
                        <td>
                          {skill.origin ? (
                            <a
                              className="table-secondary"
                              href={originURL(skill.origin)}
                              onClick={(event) => event.stopPropagation()}
                              rel="noopener noreferrer"
                              target="_blank"
                            >
                              {originLabel(skill.origin)}
                            </a>
                          ) : (
                            <StatusBadge
                              text={skill.removable ? "Created here" : "Skills directory"}
                              tone="muted"
                            />
                          )}
                        </td>
                        <td>
                          <div className="row-actions" onClick={(event) => event.stopPropagation()}>
                            <CopyButton ariaLabel="Copy prompt name" title="Copy prompt name" value={skill.prompt_name} />
                            <button
                              className="secondary-action"
                              disabled={busy[`edit-load:${skill.name}`]}
                              onClick={() => void openEdit(skill)}
                              type="button"
                            >
                              {busy[`edit-load:${skill.name}`] ? "Loading..." : "Edit"}
                            </button>
                            {skill.origin ? (
                              <button
                                className="secondary-action"
                                disabled={busy[`update:${skill.name}`]}
                                onClick={() =>
                                  void run(`update:${skill.name}`, async () => {
                                    await api.updateSkill(skill.name);
                                    return `${skill.name} updated from ${originLabel(skill.origin!)}.`;
                                  })
                                }
                                title="Reinstall from the source it was installed from"
                                type="button"
                              >
                                {busy[`update:${skill.name}`] ? "Updating..." : "Update"}
                              </button>
                            ) : null}
                            {skill.removable ? (
                              <button
                                className="danger-action"
                                disabled={busy[`remove:${skill.name}`]}
                                onClick={() => removeSkill(skill)}
                                type="button"
                              >
                                {busy[`remove:${skill.name}`] ? "Removing..." : "Remove"}
                              </button>
                            ) : null}
                          </div>
                        </td>
                      </tr>
                      {isExpanded ? (
                        <tr className="tool-expanded-row">
                          <td className="tool-expanded-cell" colSpan={5}>
                            <div className="tool-detail-panel">
                              <div className="tool-detail-header">
                                <p className="panel-label">Skill details</p>
                                <button
                                  className="secondary-action"
                                  disabled={busy[`edit-load:${skill.name}`]}
                                  onClick={() => void openEdit(skill)}
                                  type="button"
                                >
                                  Edit
                                </button>
                              </div>
                              <dl className="tool-detail-meta">
                                <div className="tool-detail-description">
                                  <dt>Description</dt>
                                  <dd className="skill-description-text">{skill.description}</dd>
                                </div>
                                {skill.license ? (
                                  <div>
                                    <dt>License</dt>
                                    <dd>{skill.license}</dd>
                                  </div>
                                ) : null}
                                {skill.origin ? (
                                  <div>
                                    <dt>Installed</dt>
                                    <dd>{new Date(skill.origin.installed_at).toLocaleString()}</dd>
                                  </div>
                                ) : null}
                              </dl>
                              {detail ? (
                                <>
                                  <div className="tool-schema-section">
                                    <div className="tool-schema-header">
                                      <h4>Bundled files</h4>
                                    </div>
                                    {detail.files.length > 0 ? (
                                      <ul className="skill-file-list">
                                        {detail.files.map((file) => (
                                          <li key={file}>
                                            <code>{file}</code>
                                          </li>
                                        ))}
                                        {detail.files_truncated ? <li>...</li> : null}
                                      </ul>
                                    ) : (
                                      <p className="empty-inline">Only SKILL.md.</p>
                                    )}
                                  </div>
                                  <details className="raw-schema-disclosure" open>
                                    <summary>Instructions (SKILL.md)</summary>
                                    <pre className="schema-code skill-instructions">
                                      <code>{detail.instructions}</code>
                                    </pre>
                                  </details>
                                </>
                              ) : (
                                <p className="empty-inline">Loading skill details...</p>
                              )}
                            </div>
                          </td>
                        </tr>
                      ) : null}
                    </Fragment>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {data.install_dir ? (
          <p className="skills-install-dir">
            Installed skills are stored in <code>{data.install_dir}</code>
          </p>
        ) : null}
      </SectionCard>

      {dialog?.kind === "add" ? (
        <div className="modal-backdrop" onClick={closeDialog} role="presentation">
          <section className={`modal-panel ${addTab === "create" ? "modal-panel-wide" : ""}`} onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <p className="panel-label">Skills</p>
                <h2>Add Skill</h2>
              </div>
              <button className="secondary-action" onClick={closeDialog} type="button">
                Close
              </button>
            </div>

            <div className="skill-mode-tabs" role="tablist">
              <button
                aria-selected={addTab === "create"}
                className={`skill-mode-tab ${addTab === "create" ? "is-active" : ""}`}
                onClick={() => setDialog({ kind: "add", tab: "create" })}
                type="button"
              >
                Create
              </button>
              <button
                aria-selected={addTab === "github"}
                className={`skill-mode-tab ${addTab === "github" ? "is-active" : ""}`}
                onClick={() => setDialog({ kind: "add", tab: "github" })}
                type="button"
              >
                From GitHub
              </button>
            </div>

            {addTab === "create" ? (
              <>
                <div className="modal-form">
                  <SkillWriteFields form={writeForm} nameReadOnly={false} onChange={patchWriteForm} />
                  {writeError ? <p className="form-error">{writeError}</p> : null}
                </div>
                <div className="modal-footer">
                  <button className="secondary-action" onClick={closeDialog} type="button">
                    Cancel
                  </button>
                  <button className="primary-action" disabled={busy.create} onClick={() => void submitCreate()} type="button">
                    {busy.create ? "Creating..." : "Create skill"}
                  </button>
                </div>
              </>
            ) : (
              <>
                <div className="modal-form">
                  <label className="form-field">
                    <span>GitHub repository or directory</span>
                    <div className="skill-source-row">
                      <input
                        autoFocus
                        className="table-filter form-input"
                        onChange={(event) => setSource(event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === "Enter") {
                            void findSkills();
                          }
                        }}
                        placeholder="owner/repo or https://github.com/owner/repo/tree/main/skills"
                        value={source}
                      />
                      <button
                        className="secondary-action"
                        disabled={busy.preview}
                        onClick={() => void findSkills()}
                        type="button"
                      >
                        {busy.preview ? "Searching..." : "Find skills"}
                      </button>
                    </div>
                  </label>
                  {!preview ? (
                    <div className="skill-examples">
                      <span>Try:</span>
                      {exampleSources.map((example) => (
                        <button className="skill-example-chip" key={example} onClick={() => setSource(example)} type="button">
                          <code>{example}</code>
                        </button>
                      ))}
                    </div>
                  ) : null}

                  {preview ? (
                    <div className="tool-group-selector panel">
                      <div className="tool-group-selector-header">
                        <strong>
                          {preview.skills.length} skill(s) in <code>{preview.source}</code>
                        </strong>
                        <button
                          className="secondary-action"
                          onClick={() =>
                            setSelected(
                              selected.length === selectableCandidates.length
                                ? []
                                : selectableCandidates.map((candidate) => candidate.name),
                            )
                          }
                          type="button"
                        >
                          {selected.length === selectableCandidates.length ? "Clear selection" : "Select all"}
                        </button>
                      </div>
                      {preview.skills.length > 6 ? (
                        <input
                          className="table-filter compact-filter"
                          onChange={(event) => setCandidateFilter(event.target.value)}
                          placeholder="Filter skills"
                          value={candidateFilter}
                        />
                      ) : null}
                      <div className="tool-pick-list">
                        {filteredCandidates.map((candidate) => {
                          const isSelected = selected.includes(candidate.name);
                          return (
                            <button
                              className={`tool-pick-item skill-candidate ${isSelected ? "is-selected" : ""}`}
                              disabled={Boolean(candidate.conflict)}
                              key={candidate.name}
                              onClick={() => toggleCandidate(candidate.name)}
                              title={candidate.conflict || candidate.path}
                              type="button"
                            >
                              <div className="skill-candidate-head">
                                <input checked={isSelected} readOnly tabIndex={-1} type="checkbox" />
                                <span className="table-primary">{candidate.name}</span>
                                {candidate.installed && !candidate.conflict ? (
                                  <StatusBadge text="Installed" tone="good" />
                                ) : null}
                                {candidate.conflict ? <StatusBadge text="Unavailable" tone="warn" /> : null}
                              </div>
                              <div className="clamped-description">{candidate.conflict || candidate.description}</div>
                            </button>
                          );
                        })}
                      </div>
                      {selectedNeedsOverwrite ? (
                        <label className="skill-overwrite">
                          <input checked={overwrite} onChange={(event) => setOverwrite(event.target.checked)} type="checkbox" />
                          <span>Replace skills that are already installed</span>
                        </label>
                      ) : null}
                    </div>
                  ) : null}

                  {addError ? <p className="form-error">{addError}</p> : null}
                </div>

                <div className="modal-footer">
                  <button className="secondary-action" onClick={closeDialog} type="button">
                    Cancel
                  </button>
                  <button
                    className="primary-action"
                    disabled={
                      !preview || selected.length === 0 || busy.install || (selectedNeedsOverwrite && !overwrite)
                    }
                    onClick={() => void installSelected()}
                    type="button"
                  >
                    {busy.install
                      ? "Installing..."
                      : selected.length > 0
                        ? `Install ${selected.length} skill${selected.length === 1 ? "" : "s"}`
                        : "Install"}
                  </button>
                </div>
              </>
            )}
          </section>
        </div>
      ) : null}

      {dialog?.kind === "edit" ? (
        <div className="modal-backdrop" onClick={closeDialog} role="presentation">
          <section className="modal-panel modal-panel-wide" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <p className="panel-label">Skills</p>
                <h2>Edit {dialog.name}</h2>
              </div>
              <button className="secondary-action" onClick={closeDialog} type="button">
                Close
              </button>
            </div>
            <div className="modal-form">
              {editOrigin ? (
                <p className="skill-edit-warning">
                  Saving writes SKILL.md on this server. <strong>Update</strong> from {originLabel(editOrigin)} will
                  overwrite these edits.
                </p>
              ) : null}
              <SkillWriteFields form={writeForm} nameReadOnly onChange={patchWriteForm} />
              {writeError ? <p className="form-error">{writeError}</p> : null}
            </div>
            <div className="modal-footer">
              <button className="secondary-action" onClick={closeDialog} type="button">
                Cancel
              </button>
              <button className="primary-action" disabled={busy.edit} onClick={() => void submitEdit()} type="button">
                {busy.edit ? "Saving..." : "Save"}
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
}
