export const themeStorageKey = "see-agent:theme";

export interface WebTheme {
  id: string;
  name: string;
  description: string;
  decoration: "none" | "verdant-habitat";
}

// This registry is the build-time plugin boundary. A new theme supplies one
// entry here and scoped CSS tokens; business UI does not import theme colors.
export const webThemes = [
  {
    id: "verdant-habitat",
    name: "绿野共生",
    description: "森林、叶脉与授粉者的安静工作台",
    decoration: "verdant-habitat",
  },
  {
    id: "quiet-paper",
    name: "静纸工作室",
    description: "克制的浅色阅读界面",
    decoration: "none",
  },
] as const satisfies readonly WebTheme[];

export type ThemeID = (typeof webThemes)[number]["id"];

export const defaultThemeID: ThemeID = "verdant-habitat";

export function isThemeID(value: string | null | undefined): value is ThemeID {
  return webThemes.some((theme) => theme.id === value);
}

export function resolveThemeID(value: string | null | undefined): ThemeID {
  return isThemeID(value) ? value : defaultThemeID;
}

export function getTheme(themeID: ThemeID): (typeof webThemes)[number] {
  return webThemes.find((theme) => theme.id === themeID) ?? webThemes[0];
}
