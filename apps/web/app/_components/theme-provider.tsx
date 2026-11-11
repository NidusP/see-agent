"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useSyncExternalStore, type ReactNode } from "react";

import { defaultThemeID, resolveThemeID, themeStorageKey, type ThemeID } from "../_lib/themes";

interface ThemeContextValue {
  themeID: ThemeID;
  setThemeID: (themeID: ThemeID) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);
const themeChangeEvent = "see-agent:theme-change";
let memoryThemeID: ThemeID = defaultThemeID;

export function ThemeProvider({ children }: { children: ReactNode }) {
  const themeID = useSyncExternalStore(subscribeToTheme, readStoredTheme, () => defaultThemeID);

  useEffect(() => {
    applyTheme(themeID);
  }, [themeID]);

  const setThemeID = useCallback((nextThemeID: ThemeID) => {
    const resolved = resolveThemeID(nextThemeID);
    memoryThemeID = resolved;
    try {
      window.localStorage.setItem(themeStorageKey, resolved);
    } catch {
      // Privacy mode/storage restrictions do not prevent an in-memory switch.
    }
    window.dispatchEvent(new Event(themeChangeEvent));
  }, []);

  const value = useMemo(() => ({ themeID, setThemeID }), [themeID, setThemeID]);
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) {
    throw new Error("useTheme must be used within ThemeProvider");
  }
  return context;
}

function readStoredTheme(): ThemeID {
  try {
    return resolveThemeID(window.localStorage.getItem(themeStorageKey) ?? memoryThemeID);
  } catch {
    return memoryThemeID;
  }
}

function applyTheme(themeID: ThemeID) {
  document.documentElement.dataset.theme = themeID;
}

function subscribeToTheme(listener: () => void) {
  window.addEventListener(themeChangeEvent, listener);
  window.addEventListener("storage", listener);
  return () => {
    window.removeEventListener(themeChangeEvent, listener);
    window.removeEventListener("storage", listener);
  };
}
