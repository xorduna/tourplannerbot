import { Editor, type JSONContent } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Show, createEffect, createSignal, onCleanup, onMount } from "solid-js";
import { render } from "solid-js/web";
import { getTelegramWebApp, type TelegramSafeAreaInset, type TelegramWebApp } from "./telegram";
import "./styles.css";

interface AuthenticatedTelegramUser {
  id: number;
  first_name: string;
  last_name?: string;
  username?: string;
}

interface SessionResponse {
  user: AuthenticatedTelegramUser;
}

interface Draft {
  id: string;
  kind: "email" | "whatsapp" | "generic";
  subject: string | null;
  content: JSONContent;
  body_text: string;
  revision: number;
  updated_at: string;
}

interface GmailReplyCandidate {
  message_id: string;
  subject?: string;
  from?: string;
  to?: string;
  date?: string;
  snippet?: string;
}

interface GmailOptions {
  reply_available: boolean;
  recipient?: string;
  candidates: GmailReplyCandidate[];
}

function draftReferenceFromLaunch(): string | null {
  const launchParameters = new URLSearchParams(window.location.search);
  const directReference = launchParameters.get("draft");
  if (directReference) return directReference;

  const startParameter = launchParameters.get("tgWebAppStartParam");
  return startParameter?.startsWith("draft_") ? startParameter.slice("draft_".length) : null;
}

const draftReference = draftReferenceFromLaunch();

function setSafeAreaVariables(inset: TelegramSafeAreaInset | undefined, prefix: string): void {
  const root = document.documentElement;
  root.style.setProperty(`--${prefix}-top`, `${inset?.top ?? 0}px`);
  root.style.setProperty(`--${prefix}-right`, `${inset?.right ?? 0}px`);
  root.style.setProperty(`--${prefix}-bottom`, `${inset?.bottom ?? 0}px`);
  root.style.setProperty(`--${prefix}-left`, `${inset?.left ?? 0}px`);
}

function applyTelegramAppearance(telegramWebApp: TelegramWebApp | undefined): void {
  const root = document.documentElement;
  const themeParameters = telegramWebApp?.themeParams ?? {};

  for (const [name, value] of Object.entries(themeParameters)) {
    if (value) {
      root.style.setProperty(`--telegram-${name.replaceAll("_", "-")}`, value);
    }
  }

  root.dataset.colorScheme = telegramWebApp?.colorScheme ?? "light";
  root.style.setProperty("--app-viewport-height", `${telegramWebApp?.viewportStableHeight ?? window.innerHeight}px`);
  setSafeAreaVariables(telegramWebApp?.safeAreaInset, "telegram-safe-area");
  setSafeAreaVariables(telegramWebApp?.contentSafeAreaInset, "telegram-content-safe-area");
}

function draftKindIcon(kind: Draft["kind"]): string {
  switch (kind) {
    case "email": return "✉";
    case "whatsapp": return "💬";
    case "generic": return "📝";
  }
}

function App() {
  const telegramWebApp = getTelegramWebApp();
  const isInsideTelegram = Boolean(telegramWebApp?.initData);
  const [viewportHeight, setViewportHeight] = createSignal(window.innerHeight);
  const [authenticatedUser, setAuthenticatedUser] = createSignal<AuthenticatedTelegramUser>();
  const [authenticationError, setAuthenticationError] = createSignal<string>();
  const [draft, setDraft] = createSignal<Draft>();
  const [draftError, setDraftError] = createSignal<string>();
  const [editorElement, setEditorElement] = createSignal<HTMLDivElement>();
  const [draftSubject, setDraftSubject] = createSignal("");
  const [isDirty, setIsDirty] = createSignal(false);
  const [isSaving, setIsSaving] = createSignal(false);
  const [saveError, setSaveError] = createSignal<string>();
  const [hasSaved, setHasSaved] = createSignal(false);
  const [copyStatus, setCopyStatus] = createSignal<string>();
  const [gmailOptions, setGmailOptions] = createSignal<GmailOptions>();
  const [isGmailDialogOpen, setIsGmailDialogOpen] = createSignal(false);
  const [isLoadingGmailOptions, setIsLoadingGmailOptions] = createSignal(false);
  const [gmailMode, setGmailMode] = createSignal<"new" | "reply">("new");
  const [gmailRecipient, setGmailRecipient] = createSignal("");
  const [replyMessageID, setReplyMessageID] = createSignal<string>();
  const [gmailError, setGmailError] = createSignal<string>();
  const [isCreatingGmailDraft, setIsCreatingGmailDraft] = createSignal(false);
  const [gmailDraftCreated, setGmailDraftCreated] = createSignal(false);
  let editor: Editor | undefined;

  createEffect(() => {
    const currentDraft = draft();
    const currentEditorElement = editorElement();
    if (!currentDraft || !currentEditorElement) return;

    const editableEditor = new Editor({
      element: currentEditorElement,
      extensions: [StarterKit],
      content: currentDraft.content,
      onUpdate: () => {
        setIsDirty(true);
        setHasSaved(false);
        setSaveError(undefined);
        setCopyStatus(undefined);
      },
    });
    editor = editableEditor;
    onCleanup(() => {
      if (editor === editableEditor) editor = undefined;
      editableEditor.destroy();
    });
  });

  createEffect(() => {
    if (!isDirty()) return;
    const preventAccidentalClose = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", preventAccidentalClose);
    onCleanup(() => window.removeEventListener("beforeunload", preventAccidentalClose));
  });

  createEffect(() => {
    if (!telegramWebApp?.enableClosingConfirmation || !telegramWebApp?.disableClosingConfirmation) return;
    if (isDirty()) {
      telegramWebApp.enableClosingConfirmation();
      onCleanup(() => telegramWebApp.disableClosingConfirmation?.());
    } else {
      telegramWebApp.disableClosingConfirmation();
    }
  });

  onMount(() => {
    telegramWebApp?.ready();
    telegramWebApp?.expand();

    const updateAppearance = () => {
      applyTelegramAppearance(telegramWebApp);
      setViewportHeight(telegramWebApp?.viewportStableHeight ?? window.innerHeight);
    };

    updateAppearance();
    window.addEventListener("resize", updateAppearance);
    onCleanup(() => window.removeEventListener("resize", updateAppearance));

    if (!telegramWebApp?.initData) {
      return;
    }
    void authenticateAndLoadDraft(telegramWebApp.initData);
  });

  async function authenticateAndLoadDraft(initData: string): Promise<void> {
    try {
      const sessionResponse = await fetch("/api/miniapp/session", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ init_data: initData }),
      });
      if (!sessionResponse.ok) {
        throw new Error("Telegram could not verify this editor session.");
      }
      const session = (await sessionResponse.json()) as SessionResponse;
      setAuthenticatedUser(session.user);

      if (!draftReference) return;
      const draftResponse = await fetch(`/api/drafts/${encodeURIComponent(draftReference)}`, { credentials: "same-origin" });
      if (draftResponse.status === 401 || draftResponse.status === 403) {
        throw new Error("You are not allowed to open this draft.");
      }
      if (draftResponse.status === 404) {
        setDraftError("This draft does not exist or is not available to you.");
        return;
      }
      if (!draftResponse.ok) {
        throw new Error("The draft could not be loaded. Please try again.");
      }
      loadDraft((await draftResponse.json()) as Draft);
    } catch (error: unknown) {
      setAuthenticationError(error instanceof Error ? error.message : "Telegram authentication failed.");
    }
  }

  function loadDraft(loadedDraft: Draft): void {
    setDraft(loadedDraft);
    setDraftSubject(loadedDraft.subject ?? "");
    setIsDirty(false);
    setHasSaved(false);
    setCopyStatus(undefined);
  }

  async function saveDraft(): Promise<void> {
    const currentDraft = draft();
    if (!currentDraft || !editor || isSaving() || !isDirty()) return;

    setIsSaving(true);
    setSaveError(undefined);
    setHasSaved(false);
    try {
      const response = await fetch(`/api/drafts/${encodeURIComponent(currentDraft.id)}`, {
        method: "PATCH",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          expected_revision: currentDraft.revision,
          subject: draftSubject().trim() || null,
          content: editor.getJSON(),
        }),
      });
      if (response.status === 409) {
        const conflict = (await response.json()) as { current: Draft };
        loadDraft(conflict.current);
        setSaveError("A newer version was saved elsewhere. The current version has been loaded.");
        return;
      }
      if (!response.ok) {
        throw new Error("Your changes could not be saved. Please try again.");
      }
      loadDraft((await response.json()) as Draft);
      setHasSaved(true);
    } catch (error: unknown) {
      setSaveError(error instanceof Error ? error.message : "Your changes could not be saved.");
    } finally {
      setIsSaving(false);
    }
  }

  function updateSubject(subject: string): void {
    setDraftSubject(subject);
    setIsDirty(true);
    setHasSaved(false);
    setSaveError(undefined);
    setCopyStatus(undefined);
  }

  async function copyDraftText(): Promise<void> {
    if (!editor) return;

    const text = editor.getText({ blockSeparator: "\n\n" }).trim();
    if (!text) {
      setCopyStatus("There is no text to copy.");
      return;
    }

    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const textArea = document.createElement("textarea");
        textArea.value = text;
        textArea.setAttribute("readonly", "");
        textArea.style.position = "fixed";
        textArea.style.opacity = "0";
        document.body.append(textArea);
        textArea.select();
        const copied = document.execCommand("copy");
        textArea.remove();
        if (!copied) throw new Error("Clipboard access was denied.");
      }
      setCopyStatus("Copied to clipboard.");
    } catch {
      setCopyStatus("Could not copy the text. Please try again.");
    }
  }

  async function openGmailDialog(): Promise<void> {
    const currentDraft = draft();
    if (!currentDraft || isDirty()) return;

    setGmailError(undefined);
    setGmailDraftCreated(false);
    setGmailOptions(undefined);
    setGmailMode("new");
    setReplyMessageID(undefined);
    setIsGmailDialogOpen(true);
    setIsLoadingGmailOptions(true);
    try {
      const response = await fetch(`/api/drafts/${encodeURIComponent(currentDraft.id)}/gmail-options`, { credentials: "same-origin" });
      if (!response.ok) throw new Error("No s’han pogut carregar les opcions de Gmail.");
      const options = (await response.json()) as GmailOptions;
      setGmailOptions(options);
      setGmailRecipient(options.recipient ?? "");
      setReplyMessageID(options.candidates[0]?.message_id);
    } catch (error: unknown) {
      setGmailError(error instanceof Error ? error.message : "No s’han pogut carregar les opcions de Gmail.");
    } finally {
      setIsLoadingGmailOptions(false);
    }
  }

  async function createGmailDraft(): Promise<void> {
    const currentDraft = draft();
    if (!currentDraft || isCreatingGmailDraft()) return;
    const mode = gmailMode();
    if (mode === "new" && !gmailRecipient().trim()) {
      setGmailError("Indica el destinatari.");
      return;
    }
    if (mode === "reply" && !replyMessageID()) {
      setGmailError("Selecciona una conversa del deal.");
      return;
    }

    setIsCreatingGmailDraft(true);
    setGmailError(undefined);
    try {
      const response = await fetch(`/api/drafts/${encodeURIComponent(currentDraft.id)}/gmail-draft`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          mode,
          to: gmailRecipient().trim(),
          message_id: mode === "reply" ? replyMessageID() : "",
        }),
      });
      if (!response.ok) {
        const payload = (await response.json().catch(() => undefined)) as { error?: string } | undefined;
        throw new Error(payload?.error ?? "Gmail no ha pogut crear l’esborrany.");
      }
      setIsGmailDialogOpen(false);
      setGmailDraftCreated(true);
    } catch (error: unknown) {
      setGmailError(error instanceof Error ? error.message : "Gmail no ha pogut crear l’esborrany.");
    } finally {
      setIsCreatingGmailDraft(false);
    }
  }

  const identityLabel = () => {
    const user = authenticatedUser();
    if (!user) return "";
    const fullName = `${user.first_name} ${user.last_name ?? ""}`.trim();
    return user.username ? `${fullName} (@${user.username})` : fullName;
  };

  return (
    <main class="shell" style={{ height: `${viewportHeight()}px` }}>
      <section class="workspace" aria-labelledby="miniapp-title">
        <header class="workspace-header">
          <div class="title-row">
            <h1 id="miniapp-title">
              <Show when={draft()}>{(loadedDraft) => <span class="draft-kind-icon" aria-hidden="true">{draftKindIcon(loadedDraft().kind)}</span>}</Show>
              {draft() ? "Draft editor" : "Mini App connected"}
            </h1>
            <Show when={draft()}>
              {(loadedDraft) => (
                <>
                  <div class="draft-meta">
                    <span>{loadedDraft().kind}</span>
                    <span>Revision {loadedDraft().revision}</span>
                  </div>
                  <div class="draft-actions" aria-label="Draft actions">
                    <button type="button" title="Copy all text" aria-label="Copy all text" onClick={() => void copyDraftText()}>
                      <svg class="copy-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 7.5A2.5 2.5 0 0 1 11.5 5h6A2.5 2.5 0 0 1 20 7.5v9a2.5 2.5 0 0 1-2.5 2.5h-6A2.5 2.5 0 0 1 9 16.5v-9Z" /><path d="M15 5V4.5A2.5 2.5 0 0 0 12.5 2h-6A2.5 2.5 0 0 0 4 4.5v9A2.5 2.5 0 0 0 6.5 16H9" /></svg>
                    </button>
                    <Show when={loadedDraft().kind === "email"}>
                      <button type="button" class="toolbar-gmail-button" aria-label="Passa a Gmail" title={isDirty() ? "Desa els canvis abans de passar-lo a Gmail" : "Passa a Gmail"} disabled={isDirty()} onClick={() => void openGmailDialog()}>
                        <svg class="gmail-icon" viewBox="0 0 24 18" aria-hidden="true">
                          <path fill="#EA4335" d="M1 3.5 12 11l11-7.5V16a2 2 0 0 1-2 2h-2V8.7l-7 4.8-7-4.8V18H3a2 2 0 0 1-2-2V3.5Z" />
                          <path fill="#34A853" d="M5 8.7 12 13.5l7-4.8V18H5V8.7Z" />
                          <path fill="#4285F4" d="M1 3.5 5 6.2V18H3a2 2 0 0 1-2-2V3.5Z" />
                          <path fill="#FBBC04" d="M23 3.5 19 6.2V18h2a2 2 0 0 0 2-2V3.5Z" />
                        </svg>
                      </button>
                    </Show>
                    <button class="toolbar-save-button" type="button" title="Save" aria-label="Save" disabled={!isDirty() || isSaving()} onClick={() => void saveDraft()}>
                      {isSaving() ? "…" : "💾"}
                    </button>
                  </div>
                </>
              )}
            </Show>
          </div>
        </header>
        <Show when={draft()}>
          {(loadedDraft) => (
            <article class="draft-editor" aria-label="Draft editor">
              <div class="subject-field">
                <label for="draft-subject">Subject</label>
                <input id="draft-subject" class="subject-input" aria-label="Draft subject" maxlength="200" placeholder="Subject" value={draftSubject()} onInput={(event) => updateSubject(event.currentTarget.value)} />
              </div>
              <div class="editor-toolbar" aria-label="Formatting controls">
                <button type="button" title="Bold" onClick={() => editor?.chain().focus().toggleBold().run()}><strong>B</strong></button>
                <button type="button" title="Italic" onClick={() => editor?.chain().focus().toggleItalic().run()}><em>I</em></button>
                <button type="button" title="Bulleted list" onClick={() => editor?.chain().focus().toggleBulletList().run()}>• List</button>
                <button type="button" title="Numbered list" onClick={() => editor?.chain().focus().toggleOrderedList().run()}>1. List</button>
                <span class="toolbar-spacer" />
                <button type="button" title="Undo" aria-label="Undo" onClick={() => editor?.chain().focus().undo().run()}>↶</button>
                <button type="button" title="Redo" aria-label="Redo" onClick={() => editor?.chain().focus().redo().run()}>↷</button>
              </div>
              <div class="tiptap-editor" ref={setEditorElement} />
            </article>
          )}
        </Show>

        <Show when={isGmailDialogOpen()}>
          <div class="gmail-dialog-backdrop" role="presentation" onClick={() => !isCreatingGmailDraft() && setIsGmailDialogOpen(false)}>
            <section class="gmail-dialog" role="dialog" aria-modal="true" aria-labelledby="gmail-dialog-title" onClick={(event) => event.stopPropagation()}>
              <h2 id="gmail-dialog-title">Passa l’esborrany a Gmail</h2>
              <div class="gmail-mode-choice">
                <label><input type="radio" name="gmail-mode" checked={gmailMode() === "new"} onChange={() => setGmailMode("new")} /> Nou correu</label>
                <Show when={gmailOptions()?.reply_available}>
                  <label><input type="radio" name="gmail-mode" checked={gmailMode() === "reply"} onChange={() => setGmailMode("reply")} /> Resposta a una conversa amb el contacte</label>
                </Show>
              </div>
              <Show when={isLoadingGmailOptions()}>
                <p class="gmail-options-loading" role="status" aria-live="polite"><span aria-hidden="true" /> Buscant converses amb aquest contacte…</p>
              </Show>
              <Show when={gmailMode() === "new"}>
                <label class="gmail-recipient-field" for="gmail-recipient">Destinatari
                  <input id="gmail-recipient" type="email" value={gmailRecipient()} placeholder="client@example.com" onInput={(event) => setGmailRecipient(event.currentTarget.value)} />
                </label>
              </Show>
              <Show when={gmailMode() === "reply"}>
                <div class="gmail-candidates" aria-label="Converses amb el contacte">
                  <Show when={gmailOptions()?.candidates.length} fallback={<p>No hi ha converses amb aquest contacte disponibles per respondre.</p>}>
                    {gmailOptions()!.candidates.map((candidate) => (
                      <label class="gmail-candidate" classList={{ selected: replyMessageID() === candidate.message_id }}>
                        <input type="radio" name="reply-message" checked={replyMessageID() === candidate.message_id} onChange={() => setReplyMessageID(candidate.message_id)} />
                        <span><strong>{candidate.subject || "Sense assumpte"}</strong><small>{candidate.from || candidate.to || "Contacte"}{candidate.date ? ` · ${candidate.date}` : ""}</small><Show when={candidate.snippet}><em>{candidate.snippet}</em></Show></span>
                      </label>
                    ))}
                  </Show>
                </div>
              </Show>
              <Show when={gmailError()}><p class="gmail-dialog-error">{gmailError()}</p></Show>
              <p class="gmail-dialog-note">Es crearà un draft a Gmail. Els canvis posteriors a Gmail no se sincronitzaran amb el bot.</p>
              <div class="gmail-dialog-actions">
                <button type="button" onClick={() => setIsGmailDialogOpen(false)} disabled={isCreatingGmailDraft()}>Cancel·la</button>
                <button type="button" class="gmail-create-button" onClick={() => void createGmailDraft()} disabled={isCreatingGmailDraft()}>{isCreatingGmailDraft() ? "Creant…" : "Crear a Gmail"}</button>
              </div>
            </section>
          </div>
        </Show>
      </section>
      <footer class="status" classList={{ development: !isInsideTelegram, error: Boolean(authenticationError() || draftError() || saveError() || gmailError()) }}>
        <span class="status-dot" aria-hidden="true" />
        <span>
          {!isInsideTelegram && "Development mode — opened outside Telegram"}
          {isInsideTelegram && !authenticatedUser() && !authenticationError() && "Verifying Telegram session…"}
          {authenticatedUser() && !draft() && !draftError() && `Authenticated as ${identityLabel()}`}
          {draftError()}
          {authenticationError()}
          {saveError()}
          {gmailDraftCreated() && "Esborrany creat a Gmail"}
          {authenticatedUser() && draft() && !draftError() && !authenticationError() && !saveError() && !gmailError() && !gmailDraftCreated() && !copyStatus() && !isSaving() && !hasSaved() && (isDirty() ? "Unsaved changes" : `Authenticated as ${identityLabel()} · Revision ${draft()!.revision}`)}
          {isSaving() && "Saving changes…"}
          {hasSaved() && !copyStatus() && "Saved"}
          {copyStatus()}
        </span>
      </footer>
    </main>
  );
}

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Mini App root element is missing");
}

render(() => <App />, rootElement);
