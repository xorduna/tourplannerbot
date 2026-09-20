import { createSignal, onCleanup, onMount } from "solid-js";
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
    void fetch("/api/miniapp/session", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ init_data: telegramWebApp.initData }),
    })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error("Telegram could not verify this editor session.");
        }
        return (await response.json()) as SessionResponse;
      })
      .then((session) => setAuthenticatedUser(session.user))
      .catch((error: unknown) => {
        setAuthenticationError(error instanceof Error ? error.message : "Telegram authentication failed.");
      });
  });

  const identityLabel = () => {
    const user = authenticatedUser();
    if (!user) return "";
    const fullName = `${user.first_name} ${user.last_name ?? ""}`.trim();
    return user.username ? `${fullName} (@${user.username})` : fullName;
  };

  return (
    <main class="shell" style={{ "min-height": `${viewportHeight()}px` }}>
      <section class="card" aria-labelledby="miniapp-title">
        <p class="eyebrow">TOUR PLANNER</p>
        <h1 id="miniapp-title">Mini App connected</h1>
        <p class="description">Your draft workspace will appear here.</p>
        <div class="status" classList={{ development: !isInsideTelegram, error: Boolean(authenticationError()) }}>
          <span class="status-dot" aria-hidden="true" />
          <span>
            {!isInsideTelegram && "Development mode — opened outside Telegram"}
            {isInsideTelegram && !authenticatedUser() && !authenticationError() && "Verifying Telegram session…"}
            {authenticatedUser() && `Authenticated as ${identityLabel()}`}
            {authenticationError()}
          </span>
        </div>
      </section>
    </main>
  );
}

const rootElement = document.getElementById("root");
if (!rootElement) {
  throw new Error("Mini App root element is missing");
}

render(() => <App />, rootElement);
