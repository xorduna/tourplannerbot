import { createSignal, onCleanup, onMount } from "solid-js";
import { render } from "solid-js/web";
import { getTelegramWebApp, type TelegramSafeAreaInset, type TelegramWebApp } from "./telegram";
import "./styles.css";

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
  });

  return (
    <main class="shell" style={{ "min-height": `${viewportHeight()}px` }}>
      <section class="card" aria-labelledby="miniapp-title">
        <p class="eyebrow">TOUR PLANNER</p>
        <h1 id="miniapp-title">Mini App connected</h1>
        <p class="description">Your draft workspace will appear here.</p>
        <div class="status" classList={{ development: !isInsideTelegram }}>
          <span class="status-dot" aria-hidden="true" />
          <span>{isInsideTelegram ? "Connected through Telegram" : "Development mode — opened outside Telegram"}</span>
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
