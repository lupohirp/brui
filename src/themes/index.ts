import { ThemeColors } from '../types';

export const catppuccinMocha: ThemeColors = {
  name: 'Catppuccin Mocha',
  isDark: true,
  bg: 'default',
  fg: '#cdd6f4',
  surface: 'default',
  surfaceHover: '#45475a',
  border: '#585b70',
  borderFocus: '#cba6f7',
  selection: '#45475a',
  selectionFg: '#cdd6f4',
  accent: '#cba6f7',      // Mauve
  subtext: '#a6adc8',     // Subtext0
  get: '#a6e3a1',         // Green
  post: '#fab387',        // Peach
  put: '#89b4fa',         // Blue
  patch: '#94e2d5',       // Teal
  delete: '#f38ba8',      // Red
  options: '#f9e2af',     // Yellow
  head: '#b4befe',        // Lavender
  statusSuccess: '#a6e3a1',
  statusWarn: '#f9e2af',
  statusError: '#f38ba8',
  statusInfo: '#89b4fa',
};

export const catppuccinMacchiato: ThemeColors = {
  name: 'Catppuccin Macchiato',
  isDark: true,
  bg: 'default',
  fg: '#cad3f5',
  surface: 'default',
  surfaceHover: '#494d64',
  border: '#5b6078',
  borderFocus: '#c6a0f6',
  selection: '#494d64',
  selectionFg: '#cad3f5',
  accent: '#c6a0f6',
  subtext: '#a5adcb',
  get: '#a6da95',
  post: '#f5a97f',
  put: '#8aadf4',
  patch: '#8bd5ca',
  delete: '#ed8796',
  options: '#eed49f',
  head: '#b7bdf8',
  statusSuccess: '#a6da95',
  statusWarn: '#eed49f',
  statusError: '#ed8796',
  statusInfo: '#8aadf4',
};

export const catppuccinFrappe: ThemeColors = {
  name: 'Catppuccin Frappé',
  isDark: true,
  bg: 'default',
  fg: '#c6d0f5',
  surface: 'default',
  surfaceHover: '#51576d',
  border: '#626880',
  borderFocus: '#ca9ee6',
  selection: '#51576d',
  selectionFg: '#c6d0f5',
  accent: '#ca9ee6',
  subtext: '#a5adce',
  get: '#a6d189',
  post: '#ef9f76',
  put: '#8caaee',
  patch: '#81c8be',
  delete: '#e78284',
  options: '#e5c890',
  head: '#babbf1',
  statusSuccess: '#a6d189',
  statusWarn: '#e5c890',
  statusError: '#e78284',
  statusInfo: '#8caaee',
};

export const catppuccinLatte: ThemeColors = {
  name: 'Catppuccin Latte',
  isDark: false,
  bg: 'default',
  fg: '#4c4f69',
  surface: 'default',
  surfaceHover: '#bcc0cc',
  border: '#acb0be',
  borderFocus: '#8839ef',
  selection: '#bcc0cc',
  selectionFg: '#4c4f69',
  accent: '#8839ef',
  subtext: '#6c6f85',
  get: '#40a02b',
  post: '#fe640b',
  put: '#1e66f5',
  patch: '#179299',
  delete: '#d20f39',
  options: '#df8e1d',
  head: '#7287fd',
  statusSuccess: '#40a02b',
  statusWarn: '#df8e1d',
  statusError: '#d20f39',
  statusInfo: '#1e66f5',
};

export const tokyoNight: ThemeColors = {
  name: 'Tokyo Night',
  isDark: true,
  bg: 'default',
  fg: '#c0caf5',
  surface: 'default',
  surfaceHover: '#292e42',
  border: '#414868',
  borderFocus: '#bb9af7',
  selection: '#33467c',
  selectionFg: '#c0caf5',
  accent: '#bb9af7',
  subtext: '#9aa5ce',
  get: '#9ece6a',
  post: '#ff9e64',
  put: '#7aa2f7',
  patch: '#7dcfff',
  delete: '#f7768e',
  options: '#e0af68',
  head: '#b4f9f8',
  statusSuccess: '#9ece6a',
  statusWarn: '#e0af68',
  statusError: '#f7768e',
  statusInfo: '#7aa2f7',
};

export const dracula: ThemeColors = {
  name: 'Dracula',
  isDark: true,
  bg: 'default',
  fg: '#f8f8f2',
  surface: 'default',
  surfaceHover: '#6272a4',
  border: '#6272a4',
  borderFocus: '#bd93f9',
  selection: '#44475a',
  selectionFg: '#f8f8f2',
  accent: '#bd93f9',
  subtext: '#bfbfbf',
  get: '#50fa7b',
  post: '#ffb86c',
  put: '#8be9fd',
  patch: '#8be9fd',
  delete: '#ff5555',
  options: '#f1fa8c',
  head: '#ff79c6',
  statusSuccess: '#50fa7b',
  statusWarn: '#f1fa8c',
  statusError: '#ff5555',
  statusInfo: '#8be9fd',
};

export const ALL_THEMES: ThemeColors[] = [
  catppuccinMocha,
  catppuccinMacchiato,
  catppuccinFrappe,
  catppuccinLatte,
  tokyoNight,
  dracula,
];

export class ThemeManager {
  private currentIndex: number = 0;

  constructor(initialThemeName: string = 'Catppuccin Mocha') {
    const idx = ALL_THEMES.findIndex(t => t.name.toLowerCase() === initialThemeName.toLowerCase());
    this.currentIndex = idx !== -1 ? idx : 0;
  }

  getCurrent(): ThemeColors {
    return ALL_THEMES[this.currentIndex];
  }

  cycle(): ThemeColors {
    this.currentIndex = (this.currentIndex + 1) % ALL_THEMES.length;
    return this.getCurrent();
  }

  setTheme(name: string): ThemeColors {
    const idx = ALL_THEMES.findIndex(t => t.name.toLowerCase() === name.toLowerCase());
    if (idx !== -1) {
      this.currentIndex = idx;
    }
    return this.getCurrent();
  }

  getAll(): ThemeColors[] {
    return ALL_THEMES;
  }
}
