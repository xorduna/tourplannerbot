export interface TelegramSafeAreaInset {
  bottom?: number;
  left?: number;
  right?: number;
  top?: number;
}

export interface TelegramWebApp {
  colorScheme?: "dark" | "light";
  contentSafeAreaInset?: TelegramSafeAreaInset;
  expand: () => void;
  initData: string;
  ready: () => void;
  safeAreaInset?: TelegramSafeAreaInset;
  themeParams: Record<string, string | undefined>;
  viewportHeight?: number;
  viewportStableHeight?: number;
}

declare global {
  interface Window {
    Telegram?: {
      WebApp?: TelegramWebApp;
    };
  }
}

// getTelegramWebApp returns the Telegram bridge when this page is opened by Telegram.
export function getTelegramWebApp(): TelegramWebApp | undefined {
  return window.Telegram?.WebApp;
}
