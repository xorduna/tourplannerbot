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
                <div class="draft-meta">
                  <span>{loadedDraft().kind}</span>
                  <span>Revision {loadedDraft().revision}</span>
                </div>
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
                <button class="toolbar-save-button" type="button" title="Save" aria-label="Save" disabled={!isDirty() || isSaving()} onClick={() => void saveDraft()}>
                  {isSaving() ? "…" : "💾"}
                </button>
              </div>
              <div class="tiptap-editor" ref={setEditorElement} />
            </article>
          )}
        </Show>
      </section>
      <footer class="status" classList={{ development: !isInsideTelegram, error: Boolean(authenticationError() || draftError() || saveError()) }}>
        <span class="status-dot" aria-hidden="true" />
        <span>
          {!isInsideTelegram && "Development mode — opened outside Telegram"}
          {isInsideTelegram && !authenticatedUser() && !authenticationError() && "Verifying Telegram session…"}
          {authenticatedUser() && !draft() && !draftError() && `Authenticated as ${identityLabel()}`}
          {draftError()}
          {authenticationError()}
          {saveError()}
          {authenticatedUser() && draft() && !draftError() && !authenticationError() && !saveError() && !isSaving() && !hasSaved() && (isDirty() ? "Unsaved changes" : `Authenticated as ${identityLabel()} · Revision ${draft()!.revision}`)}
          {isSaving() && "Saving changes…"}
          {hasSaved() && "Saved"}
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
