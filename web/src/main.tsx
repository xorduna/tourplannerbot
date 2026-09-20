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

const draftReference = new URLSearchParams(window.location.search).get("draft");

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

function App() {
  const telegramWebApp = getTelegramWebApp();
  const isInsideTelegram = Boolean(telegramWebApp?.initData);
  const [viewportHeight, setViewportHeight] = createSignal(window.innerHeight);
  const [authenticatedUser, setAuthenticatedUser] = createSignal<AuthenticatedTelegramUser>();
  const [authenticationError, setAuthenticationError] = createSignal<string>();
  const [draft, setDraft] = createSignal<Draft>();
  const [draftError, setDraftError] = createSignal<string>();
  const [editorElement, setEditorElement] = createSignal<HTMLDivElement>();

  createEffect(() => {
    const currentDraft = draft();
    const currentEditorElement = editorElement();
    if (!currentDraft || !currentEditorElement) return;

    const readOnlyEditor = new Editor({
      element: currentEditorElement,
      editable: false,
      extensions: [StarterKit],
      content: currentDraft.content,
    });
    onCleanup(() => readOnlyEditor.destroy());
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
      setDraft((await draftResponse.json()) as Draft);
    } catch (error: unknown) {
      setAuthenticationError(error instanceof Error ? error.message : "Telegram authentication failed.");
    }
  }

  const identityLabel = () => {
    const user = authenticatedUser();
    if (!user) return "";
    const fullName = `${user.first_name} ${user.last_name ?? ""}`.trim();
    return user.username ? `${fullName} (@${user.username})` : fullName;
  };

  return (
    <main class="shell" style={{ "min-height": `${viewportHeight()}px` }}>
      <section class="workspace" aria-labelledby="miniapp-title">
        <header class="workspace-header">
          <p class="eyebrow">TOUR PLANNER</p>
          <h1 id="miniapp-title">{draft() ? "Draft preview" : "Mini App connected"}</h1>
          <p class="description">{draft() ? "Read-only preview" : "Your draft workspace will appear here."}</p>
        </header>
        <Show when={draft()}>
          {(loadedDraft) => (
            <article class="draft-preview" aria-label="Draft preview">
              <div class="draft-meta">
                <span>{loadedDraft().kind}</span>
                <span>Revision {loadedDraft().revision}</span>
              </div>
              <Show when={loadedDraft().subject}>
                <h2>{loadedDraft().subject}</h2>
              </Show>
              <div class="tiptap-preview" ref={setEditorElement} />
            </article>
          )}
        </Show>
      </section>
      <footer class="status" classList={{ development: !isInsideTelegram, error: Boolean(authenticationError() || draftError()) }}>
        <span class="status-dot" aria-hidden="true" />
        <span>
          {!isInsideTelegram && "Development mode — opened outside Telegram"}
          {isInsideTelegram && !authenticatedUser() && !authenticationError() && "Verifying Telegram session…"}
          {authenticatedUser() && !draft() && !draftError() && `Authenticated as ${identityLabel()}`}
          {draftError()}
          {authenticationError()}
          {authenticatedUser() && draft() && !draftError() && !authenticationError() && `Authenticated as ${identityLabel()} · Read-only · Revision ${draft()!.revision}`}
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
