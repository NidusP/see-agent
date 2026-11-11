"use client";

import { getTheme, webThemes, type ThemeID } from "../_lib/themes";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { useTheme } from "./theme-provider";

interface ThemePickerProps {
  compact?: boolean;
}

export function ThemePicker({ compact = false }: ThemePickerProps) {
  const { themeID, setThemeID } = useTheme();
  const selected = getTheme(themeID);

  return (
    <div className={`theme-picker${compact ? " is-compact" : ""}`} title={selected.description}>
      <span className="theme-picker-label"><span aria-hidden="true">⌘</span> 界面主题</span>
      <Select
        value={themeID}
        onValueChange={(value) => setThemeID(value as ThemeID)}
      >
        <SelectTrigger aria-label="界面主题" data-testid="theme-picker" size="sm" className="min-w-36 bg-background"><SelectValue className="sr-only" /><span>{selected.name}</span></SelectTrigger>
        <SelectContent>{webThemes.map((theme) => <SelectItem key={theme.id} value={theme.id}>{theme.name}</SelectItem>)}</SelectContent>
      </Select>
    </div>
  );
}
