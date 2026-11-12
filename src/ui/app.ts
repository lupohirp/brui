import blessed from 'neo-blessed';
import * as path from 'node:path';
import * as fs from 'node:fs';
import { bruToJsonV2, jsonToBruV2 } from '@usebruno/lang';
import { BrunoCollection, BrunoFolder, BrunoRequest, HttpResponse, ConsoleLogEntry, ThemeColors, OAuth2Config } from '../types';
import { ThemeManager } from '../themes';
import { CollectionParser, DiscoveredCollection } from '../core/parser';
import { EnvironmentManagerModal } from './env-manager';
import { AuthManagerModal } from './auth-manager';
import { HttpClient } from '../core/http-client';
import { ScriptRunner } from '../core/script-runner';
import { OAuth2Handler } from '../core/oauth2';
import { Interpolator } from '../core/interpolation';

export type SidebarItem =
  | { type: 'folder'; folder: BrunoFolder; depth: number }
  | { type: 'request'; request: BrunoRequest; depth: number };

export class BruiApp {
  private collectionPath: string;
  private collection!: BrunoCollection;
  private parser!: CollectionParser;
  private themeManager: ThemeManager;
  private scriptRunner!: ScriptRunner;

  private visibleTreeItems: SidebarItem[] = [];
  private expandedFolders: Set<string> = new Set();
  private selectedTreeIndex: number = 0;
  private selectedRequest?: BrunoRequest;

  private activeRequestTab: 'headers' | 'params' | 'body' | 'auth' | 'pre-script' | 'post-script' | 'docs' = 'headers';
  private activeResponseTab: 'body' | 'headers' | 'console' = 'body';
  private lastResponse?: HttpResponse;

  private requestTabRanges: Array<{ id: 'headers' | 'params' | 'body' | 'auth' | 'pre-script' | 'post-script' | 'docs'; start: number; end: number }> = [];
  private responseTabRanges: Array<{ id: 'body' | 'headers' | 'console'; start: number; end: number }> = [];
  private authModeRanges: Array<{ id: BrunoRequest['authType']; start: number; end: number }> = [];
  private bodyTypeRanges: Array<{ id: BrunoRequest['bodyType']; start: number; end: number }> = [];

  private modifiedRequests: Set<string> = new Set();
  private isUpdatingSidebar: boolean = false;

  // UI Widgets
  private screen!: blessed.Widgets.Screen;
  private headerBox!: blessed.Widgets.BoxElement;
  private sidebarList!: blessed.Widgets.ListElement;
  private requestPane!: blessed.Widgets.BoxElement;
  private requestHeaderBox!: blessed.Widgets.BoxElement;
  private requestTabsBox!: blessed.Widgets.BoxElement;
  private requestContentBox!: blessed.Widgets.BoxElement;
  private responsePane!: blessed.Widgets.BoxElement;
  private responseHeaderBox!: blessed.Widgets.BoxElement;
  private responseTabsBox!: blessed.Widgets.BoxElement;
  private responseContentBox!: blessed.Widgets.BoxElement;
  private statusBar!: blessed.Widgets.BoxElement;

  private collectionModal?: blessed.Widgets.BoxElement;
  private editorModal?: blessed.Widgets.BoxElement;
  private envManagerModal?: EnvironmentManagerModal;
  private authManagerModal?: AuthManagerModal;
  private isRunning: boolean = false;

  constructor(collectionPath: string, themeName: string = 'Catppuccin Mocha', initialEnv?: string) {
    this.collectionPath = path.resolve(collectionPath);
    this.themeManager = new ThemeManager(themeName);
    this.loadCollection(this.collectionPath, initialEnv);
  }

  private loadCollection(targetPath: string, initialEnv?: string) {
    this.collectionPath = targetPath;
    this.parser = new CollectionParser(this.collectionPath);
    this.collection = this.parser.parse();
    if (initialEnv) {
      const found = this.collection.environments.find(e => e.name.toLowerCase() === initialEnv.toLowerCase());
      if (found) {
        this.collection.activeEnvironment = found.name;
      }
    }
    this.scriptRunner = new ScriptRunner(this.collection, this.parser.requestMap);
    this.modifiedRequests.clear();

    // Expand all folders by default so every API is immediately visible and clickable
    this.expandedFolders.clear();
    const expandAll = (folders: BrunoFolder[]) => {
      for (const f of folders) {
        this.expandedFolders.add(f.id);
        expandAll(f.folders);
      }
    };
    expandAll(this.collection.folders);

    this.rebuildTree();

    // Select first available request
    const firstReq = this.findFirstRequest(this.collection);
    this.selectedRequest = firstReq;

    if (this.screen) {
      this.envManagerModal = undefined;
    }
  }

  private findFirstRequest(c: BrunoCollection): BrunoRequest | undefined {
    if (c.requests.length > 0) return c.requests[0];
    const walk = (folders: BrunoFolder[]): BrunoRequest | undefined => {
      for (const f of folders) {
        if (f.requests.length > 0) return f.requests[0];
        const sub = walk(f.folders);
        if (sub) return sub;
      }
      return undefined;
    };
    return walk(c.folders);
  }

  private rebuildTree() {
    this.visibleTreeItems = [];

    // Root-level requests
    for (const r of this.collection.requests) {
      this.visibleTreeItems.push({ type: 'request', request: r, depth: 0 });
    }

    // Folders
    const appendFolder = (folder: BrunoFolder, depth: number) => {
      this.visibleTreeItems.push({ type: 'folder', folder, depth });
      if (this.expandedFolders.has(folder.id)) {
        for (const subF of folder.folders) {
          appendFolder(subF, depth + 1);
        }
        for (const req of folder.requests) {
          this.visibleTreeItems.push({ type: 'request', request: req, depth: depth + 1 });
        }
      }
    };

    for (const f of this.collection.folders) {
      appendFolder(f, 0);
    }
  }

  public start() {
    this.screen = blessed.screen({
      smartCSR: true,
      terminal: 'xterm-256color',
      fullUnicode: true,
      title: `brui - ${this.collection.name}`,
      mouse: true,
      sendFocus: true,
      useBCE: true,
      cursor: {
        artificial: true,
        shape: 'line',
        blink: true,
        color: 'white'
      }
    });

    this.buildLayout();
    this.applyTheme();
    this.bindEvents();

    this.updateRequestView();
    this.updateResponseView();

    this.envManagerModal = new EnvironmentManagerModal(
      this.screen,
      this.collection,
      this.themeManager,
      () => {
        this.updateHeader();
        this.updateRequestView();
        this.updateResponseView();
      }
    );

    this.sidebarList.focus();
    this.screen.render();
  }

  private getTheme(): ThemeColors {
    return this.themeManager.getCurrent();
  }

  private buildLayout() {
    const theme = this.getTheme();

    // 1. Top Header (Height 5 -> 3 inner lines for 6x9 pixel-art Bruno dog + controls)
    this.headerBox = blessed.box({
      parent: this.screen,
      top: 0,
      left: 0,
      width: '100%',
      height: 5,
      tags: true,
      border: { type: 'line' },
      mouse: true
    });

    // 2. Left Sidebar (Tree view with folders & requests)
    this.sidebarList = blessed.list({
      parent: this.screen,
      top: 5,
      left: 0,
      width: '32%',
      bottom: 2,
      border: { type: 'line' },
      tags: true,
      mouse: true,
      keys: true,
      vi: true,
      scrollable: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.bg },
        style: { bg: theme.selection }
      }
    });

    // 3. Request Pane (Top Right)
    this.requestPane = blessed.box({
      parent: this.screen,
      top: 5,
      left: '32%',
      width: '68%',
      bottom: '48%',
      border: { type: 'line' },
      tags: true
    });

    this.requestHeaderBox = blessed.box({
      parent: this.requestPane,
      top: 0,
      left: 1,
      right: 1,
      height: 2,
      tags: true,
      mouse: true
    });

    this.requestTabsBox = blessed.box({
      parent: this.requestPane,
      top: 2,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      mouse: true
    });

    this.requestContentBox = blessed.box({
      parent: this.requestPane,
      top: 4,
      left: 1,
      right: 1,
      bottom: 1,
      tags: true,
      mouse: true,
      scrollable: true,
      alwaysScroll: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.bg },
        style: { bg: theme.selection }
      }
    });

    // 4. Response Pane (Bottom Right)
    this.responsePane = blessed.box({
      parent: this.screen,
      top: '52%',
      left: '32%',
      width: '68%',
      bottom: 2,
      border: { type: 'line' },
      tags: true
    });

    this.responseHeaderBox = blessed.box({
      parent: this.responsePane,
      top: 0,
      left: 1,
      right: 1,
      height: 2,
      tags: true,
      mouse: true
    });

    this.responseTabsBox = blessed.box({
      parent: this.responsePane,
      top: 2,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      mouse: true
    });

    this.responseContentBox = blessed.box({
      parent: this.responsePane,
      top: 4,
      left: 1,
      right: 1,
      bottom: 1,
      tags: true,
      mouse: true,
      scrollable: true,
      alwaysScroll: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.bg },
        style: { bg: theme.selection }
      }
    });

    // 5. Status Bar (Bottom)
    this.statusBar = blessed.box({
      parent: this.screen,
      bottom: 0,
      left: 0,
      width: '100%',
      height: 2,
      tags: true,
      mouse: true
    });

    this.renderSidebar();
  }

  private applyTheme() {
    const t = this.getTheme();

    this.headerBox.style.bg = t.bg;
    this.headerBox.style.fg = t.fg;
    this.headerBox.style.border.fg = t.border;

    this.sidebarList.style.bg = t.bg;
    this.sidebarList.style.fg = t.fg;
    this.sidebarList.style.border.fg = t.borderFocus;
    this.sidebarList.style.selected = {
      bg: t.selection,
      fg: t.selectionFg,
      bold: true
    };
    this.sidebarList.style.item = {
      bg: t.bg,
      fg: t.fg
    };

    this.requestPane.style.bg = t.bg;
    this.requestPane.style.fg = t.fg;
    this.requestPane.style.border.fg = t.border;

    this.requestHeaderBox.style.bg = t.bg;
    this.requestHeaderBox.style.fg = t.fg;

    this.requestTabsBox.style.bg = t.bg;
    this.requestTabsBox.style.fg = t.fg;

    this.requestContentBox.style.bg = t.bg;
    this.requestContentBox.style.fg = t.fg;

    this.responsePane.style.bg = t.bg;
    this.responsePane.style.fg = t.fg;
    this.responsePane.style.border.fg = t.border;

    this.responseHeaderBox.style.bg = t.bg;
    this.responseHeaderBox.style.fg = t.fg;

    this.responseTabsBox.style.bg = t.bg;
    this.responseTabsBox.style.fg = t.fg;

    this.responseContentBox.style.bg = t.bg;
    this.responseContentBox.style.fg = t.fg;

    this.statusBar.style.bg = t.surface;
    this.statusBar.style.fg = t.fg;

    this.updateHeader();
    this.updateStatusBar('Ready');
    this.screen.render();
  }

  private updateHeader() {
    const t = this.getTheme();
    const envName = this.collection.activeEnvironment || 'None';
    const authStatus = this.collection.runtimeVariables['access_token']
      ? `{${t.get}-fg}[OK: Auth]{/}`
      : `{${t.subtext}-fg}[--: No Auth]{/}`;

    // 6x9 half-block pixel-art sprite of Bruno the Golden Retriever (3 rows x 9 cols):
    // E = floppy ears (#c97a2b), Y = gold fur (#f4aa42), K = eyes/nose (#11111b), R = tongue (#ea5a47)
    const E = '#c97a2b';
    const Y = '#f4aa42';
    const K = '#11111b';
    const R = '#ea5a47';

    const dog1 = `{${E}-fg}▄█{/}{${Y}-fg}█████{/}{${E}-fg}█▄{/}`;
    const dog2 = `{${E}-fg}▀{/}{${E}-fg}{${Y}-bg}▀{/}{${K}-fg}{${Y}-bg}▀{/}{${t.bg}-bg}{${Y}-fg}█{/}{${Y}-fg}{${K}-bg}▀{/}{${t.bg}-bg}{${Y}-fg}█{/}{${K}-fg}{${Y}-bg}▀{/}{${E}-fg}{${Y}-bg}▀{/}{${t.bg}-bg}{${E}-fg}▀{/}`;
    const dog3 = `  {${Y}-fg}▀▀{/}{${R}-fg}█{/}{${Y}-fg}▀▀{/}  `;

    const line1 = ` ${dog1}  {bold}{#f4aa42-fg}BRUI{/} {${t.subtext}-fg}- Bruno Terminal Client{/}{right}{${t.subtext}-fg}Theme [T]:{/} {bold}{${t.put}-fg}[${t.name}]{/} `;
    const line2 = ` ${dog2}  {${t.subtext}-fg}Collection [C]:{/} {bold}{${t.put}-fg}[${this.collection.name}]{/}   ` +
                  `{${t.subtext}-fg}Env [E]:{/} {bold}{${t.options}-fg}[${envName}]{/}   ` +
                  `{${t.subtext}-fg}Auth [A]:{/} ${authStatus}`;
    const line3 = ` ${dog3}  {${t.subtext}-fg}[Space] Send  [U] URL  [I] Edit Tab  [S] Save .bru  [R] Revert  [O] OAuth2{/}`;

    this.headerBox.setContent(`${line1}\n${line2}\n${line3}`);
  }

  private renderSidebar() {
    const t = this.getTheme();
    const items: string[] = [];

    for (const item of this.visibleTreeItems) {
      const indent = '  '.repeat(item.depth);
      if (item.type === 'folder') {
        const isExpanded = this.expandedFolders.has(item.folder.id);
        const marker = isExpanded ? 'v' : '>';
        const total = item.folder.requests.length + item.folder.folders.length;
        items.push(`${indent}{bold}{${t.accent}-fg}${marker} [${item.folder.name}]{/} {${t.subtext}-fg}(${total}){/}`);
      } else {
        const req = item.request;
        let badgeColor = t.get;
        if (req.method === 'POST') badgeColor = t.post;
        else if (req.method === 'PUT') badgeColor = t.put;
        else if (req.method === 'PATCH') badgeColor = t.patch;
        else if (req.method === 'DELETE') badgeColor = t.delete;

        const modStar = this.modifiedRequests.has(req.id) ? ` {bold}{${t.options}-fg}*{/}` : '';
        items.push(`${indent} {bold}{${badgeColor}-fg}${req.method.padEnd(5)}{/} ${req.name}${modStar}`);
      }
    }

    this.isUpdatingSidebar = true;
    this.sidebarList.setItems(items);
    if (this.selectedTreeIndex >= 0 && this.selectedTreeIndex < items.length) {
      (this.sidebarList as any).select(this.selectedTreeIndex);
    }
    this.isUpdatingSidebar = false;
    this.screen.render();
  }

  private updateRequestView() {
    const t = this.getTheme();
    const req = this.selectedRequest;
    if (!req) {
      this.requestHeaderBox.setContent(' No request selected. Expand a folder or select a request.');
      this.requestTabsBox.setContent('');
      this.requestContentBox.setContent('');
      this.screen.render();
      return;
    }

    let badgeColor = t.get;
    if (req.method === 'POST') badgeColor = t.post;
    else if (req.method === 'PUT') badgeColor = t.put;
    else if (req.method === 'PATCH') badgeColor = t.patch;
    else if (req.method === 'DELETE') badgeColor = t.delete;

    const isDirty = this.modifiedRequests.has(req.id);
    const modBadge = isDirty
      ? ` {bold}{${t.options}-fg}[* MODIFIED]{/}`
      : '';

    // Highlight {{VAR_1}} in theme's accent color (Catppuccin Mauve/Violet #cba6f7)
    const highlightedUrl = Interpolator.highlightVariables(req.url, this.collection, t.accent, t.delete);
    const resolvedUrlColored = Interpolator.interpolateWithColor(req.url, this.collection, t.accent, t.delete);

    const headerLine1 =
      ` {bold}{${badgeColor}-fg}[ ${req.method} ]{/}  {bold}${highlightedUrl}{/}${modBadge}` +
      `{right}{bold}{${t.put}-fg}[Edit URL (U)]{/} {bold}{${t.options}-fg}[Edit (I)]{/} {bold}{${t.get}-fg}[Save (S)]{/} {bold}{${t.delete}-fg}[Revert (R)]{/}`;
    const headerLine2 = req.url.includes('{{')
      ? ` {${t.subtext}-fg}-> ${resolvedUrlColored}{/}`
      : ` {${t.subtext}-fg}File: ${req.relativePath}{/}`;

    this.requestHeaderBox.setContent(`${headerLine1}\n${headerLine2}`);

    const tabs: Array<{ id: 'headers' | 'params' | 'body' | 'auth' | 'pre-script' | 'post-script' | 'docs'; label: string }> = [
      { id: 'headers', label: `Headers (${req.headers.length})` },
      { id: 'params', label: `Params (${req.params.length})` },
      { id: 'body', label: `Body (${req.bodyType})` },
      { id: 'auth', label: `Auth (${req.authType})` },
      { id: 'pre-script', label: `Pre-Script${req.preRequestScript ? ' *' : ''}` },
      { id: 'post-script', label: `Post-Script${req.postResponseScript ? ' *' : ''}` },
      { id: 'docs', label: `Docs` }
    ];

    this.requestTabRanges = [];
    let reqColOffset = 0;
    const tabStr = tabs.map(tab => {
      const visibleLen = tab.label.length + 4;
      this.requestTabRanges.push({
        id: tab.id,
        start: reqColOffset,
        end: reqColOffset + visibleLen - 1
      });
      reqColOffset += visibleLen + 1;
      if (tab.id === this.activeRequestTab) {
        return `{bold}{${t.accent}-fg}[ ${tab.label} ]{/}`;
      }
      return `{${t.subtext}-fg}  ${tab.label}  {/}`;
    }).join(' ');

    this.requestTabsBox.setContent(tabStr);

    let content = '';
    if (this.activeRequestTab === 'headers') {
      if (req.headers.length === 0) {
        content = `{${t.subtext}-fg}No custom headers.{/}\n\n`;
      } else {
        content = req.headers.map((h, i) => {
          const rawColored = Interpolator.highlightVariables(h.value, this.collection, t.accent, t.delete);
          const resPreview = h.value.includes('{{')
            ? `  {${t.subtext}-fg}(-> ${Interpolator.interpolateWithColor(h.value, this.collection, t.accent, t.delete)}{${t.subtext}-fg}){/}`
            : '';
          return `${i + 1}. {bold}{${t.put}-fg}${h.name}{/}: ${rawColored}${resPreview}   {${t.put}-fg}[Edit]{/} {${t.delete}-fg}[Del]{/}`;
        }).join('\n') + '\n\n';
      }
      content += `{bold}{${t.get}-fg}[ + Add Header (Press 'I' or Click) ]{/}   ` +
                 `{bold}{${t.options}-fg}[ Save .bru (Press 'S') ]{/}   ` +
                 `{bold}{${t.delete}-fg}[ Revert (Press 'R') ]{/}`;
    } else if (this.activeRequestTab === 'params') {
      if (req.params.length === 0) {
        content = `{${t.subtext}-fg}No query parameters.{/}\n\n`;
      } else {
        content = req.params.map((p, i) => {
          const rawColored = Interpolator.highlightVariables(p.value, this.collection, t.accent, t.delete);
          const resPreview = p.value.includes('{{')
            ? `  {${t.subtext}-fg}(-> ${Interpolator.interpolateWithColor(p.value, this.collection, t.accent, t.delete)}{${t.subtext}-fg}){/}`
            : '';
          return `${i + 1}. {bold}{${t.patch}-fg}${p.name}{/} = ${rawColored}${resPreview}   {${t.put}-fg}[Edit]{/} {${t.delete}-fg}[Del]{/}`;
        }).join('\n') + '\n\n';
      }
      content += `{bold}{${t.get}-fg}[ + Add Param (Press 'I' or Click) ]{/}   ` +
                 `{bold}{${t.options}-fg}[ Save .bru (Press 'S') ]{/}   ` +
                 `{bold}{${t.delete}-fg}[ Revert (Press 'R') ]{/}`;
    } else if (this.activeRequestTab === 'body') {
      const bTypes: Array<BrunoRequest['bodyType']> = ['none', 'json', 'text', 'formUrlEncoded', 'xml'];
      this.bodyTypeRanges = [];
      let bColOffset = 13; // " Body Type:  " is 13 chars
      const bTypeButtons = bTypes.map(bt => {
        const label = bt.toUpperCase();
        const visibleLen = label.length + 4;
        this.bodyTypeRanges.push({
          id: bt,
          start: bColOffset,
          end: bColOffset + visibleLen - 1
        });
        bColOffset += visibleLen + 2;
        if (bt === req.bodyType) {
          return `{bold}{${t.selectionFg}-fg}{${t.accent}-bg}[ ${label} ]{/}{${t.bg}-bg}`;
        }
        return `{${t.subtext}-fg}[ ${label} ]{/}`;
      }).join('  ');

      content = ` {bold}Body Type:{/}  ${bTypeButtons}\n`;
      content += ` {bold}{${t.put}-fg}[ Edit Body (Press 'I' or Click) ]{/}   ` +
                 `{bold}{${t.get}-fg}[ Save .bru (Press 'S') ]{/}   ` +
                 `{bold}{${t.delete}-fg}[ Revert (Press 'R') ]{/}\n\n`;

      if (req.bodyType === 'none' || !req.bodyContent) {
        content += `{${t.subtext}-fg}No body configured.{/}`;
      } else {
        content += Interpolator.highlightVariables(req.bodyContent, this.collection, t.accent, t.delete);
        if (req.bodyContent.includes('{{')) {
          content += `\n\n{${t.subtext}-fg}--- Resolved Body Preview ---{/}\n`;
          content += Interpolator.interpolateWithColor(req.bodyContent, this.collection, t.accent, t.delete);
        }
      }
    } else if (this.activeRequestTab === 'auth') {
      const mode = req.authType;
      const modes = ['inherit', 'none', 'bearer', 'basic', 'oauth2'] as const;
      this.authModeRanges = [];
      let modeColOffset = 13; // " Auth Mode:  " is 13 chars
      const modeButtons = modes.map(m => {
        const visibleLen = m.length + 4;
        this.authModeRanges.push({
          id: m,
          start: modeColOffset,
          end: modeColOffset + visibleLen - 1
        });
        modeColOffset += visibleLen + 2;
        if (m === mode) {
          return `{bold}{${t.selectionFg}-fg}{${t.accent}-bg}[ ${m.toUpperCase()} ]{/}{${t.bg}-bg}`;
        }
        return `{${t.subtext}-fg}[ ${m.toUpperCase()} ]{/}`;
      }).join('  ');

      let token: string | undefined;
      let exp: number = 0;
      if (this.collection.activeEnvironment) {
        const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
        if (env) {
          token = env.variables['access_token'];
          exp = Number(env.variables['access_token_exp']) || 0;
        }
      }
      if (!token) {
        token = this.collection.runtimeVariables['access_token'];
        exp = Number(this.collection.runtimeVariables['access_token_exp']) || 0;
      }

      const now = Date.now();
      const isExpired = exp > 0 && now > exp;
      const expSec = exp > 0 ? Math.max(0, Math.round((exp - now) / 1000)) : undefined;

      const tokenBadge = token
        ? (isExpired
            ? `{${t.statusError}-fg}[EXPIRED]{/}`
            : `{${t.statusSuccess}-fg}[ACTIVE (${expSec}s)]{/}`)
        : `{${t.subtext}-fg}[NO TOKEN]{/}`;

      content = ` {bold}Auth Mode:{/}  ${modeButtons}\n\n`;

      if (mode === 'inherit') {
        const collMode = this.collection.collectionAuthType || 'none';
        content += ` {bold}{${t.accent}-fg}Mode:{/} Inherited from collection.bru ({bold}${collMode.toUpperCase()}{/})\n`;
        if (collMode === 'bearer') {
          const rawTok = this.collection.collectionAuthBearerToken || '{{access_token}}';
          const resTok = Interpolator.interpolate(rawTok, this.collection);
          content += ` {bold}Collection Token:{/} ${Interpolator.highlightVariables(rawTok, this.collection, t.accent, t.delete)}\n`;
          content += ` {bold}Resolved Value:{/}   {${t.accent}-fg}${resTok ? resTok.slice(0, 50) + '...' : '{#6c7086-fg}(empty){/}'}{/}\n`;
        }
        content += ` {bold}Token Status:{/}     ${tokenBadge}\n\n`;
      } else if (mode === 'none') {
        content += ` {${t.subtext}-fg}No authentication credentials will be sent with this request.{/}\n\n`;
      } else if (mode === 'bearer') {
        const rawTok = req.authBearerToken || '{{access_token}}';
        const resTok = Interpolator.interpolate(rawTok, this.collection);
        content += ` {bold}{${t.accent}-fg}Bearer Token:{/}  ${Interpolator.highlightVariables(rawTok, this.collection, t.accent, t.delete)}\n`;
        content += ` {bold}Resolved Value:{/} {${t.accent}-fg}${resTok ? resTok.slice(0, 50) + '...' : '{#6c7086-fg}(empty){/}'}{/}\n`;
        content += ` {bold}Token Status:{/}   ${tokenBadge}\n\n`;
      } else if (mode === 'basic') {
        content += ` {bold}{${t.accent}-fg}Username:{/} ${Interpolator.highlightVariables(req.authBasicUser || '', this.collection, t.accent, t.delete) || '{#6c7086-fg}(empty){/}'}\n`;
        content += ` {bold}{${t.accent}-fg}Password:{/} ${req.authBasicPassword ? '********' : '{#6c7086-fg}(empty){/}'}\n\n`;
      } else if (mode === 'oauth2') {
        const o = req.authOAuth2 || { grantType: 'password', tokenUrl: '', clientId: '' };
        content += ` {bold}{${t.accent}-fg}Grant Type:{/}   {bold}{${t.options}-fg}[${o.grantType.toUpperCase()}]{/}\n`;
        content += ` {bold}Token URL:{/}    ${Interpolator.highlightVariables(o.tokenUrl || '', this.collection, t.accent, t.delete) || '{#6c7086-fg}(not set){/}'}\n`;
        content += ` {bold}Client ID:{/}    ${Interpolator.highlightVariables(o.clientId || '', this.collection, t.accent, t.delete) || '{#6c7086-fg}(not set){/}'}\n`;
        if (o.grantType === 'password') {
          content += ` {bold}Username:{/}     ${Interpolator.highlightVariables(o.username || '', this.collection, t.accent, t.delete) || '{#6c7086-fg}(not set){/}'}\n`;
          content += ` {bold}Password:{/}     ${o.password ? '********' : '{#6c7086-fg}(not set){/}'}\n`;
        } else if (o.grantType === 'authorization_code') {
          content += ` {bold}Auth URL:{/}     ${Interpolator.highlightVariables(o.authUrl || '', this.collection, t.accent, t.delete) || '{#6c7086-fg}(not set){/}'}\n`;
          content += ` {bold}Callback URL:{/} ${o.redirectUri || 'http://127.0.0.1:3000/callback'}\n`;
          content += ` {bold}PKCE:{/}         ${o.pkce !== false ? '{#a6e3a1-fg}Enabled{/}' : '{#f38ba8-fg}Disabled{/}'}\n`;
        }
        content += ` {bold}Scope:{/}        ${o.scope || 'openid'}\n`;
        content += ` {bold}Token Status:{/} ${tokenBadge}\n\n`;
      }

      content += `{bold}{${t.accent}-fg}--- Actions -------------------------------------------------------------{/}\n`;
      content += ` {bold}{${t.get}-fg}[ GET ACCESS TOKEN (Press 'O' or Click) ]{/}  \n` +
                 ` {bold}{${t.put}-fg}[ Configure Auth (Press 'A') ]{/}  ` +
                 `{bold}{${t.options}-fg}[ Cycle Mode (Press 'M') ]{/}  ` +
                 `{bold}{${t.get}-fg}[ Save (Press 'S') ]{/}  ` +
                 `{bold}{${t.delete}-fg}[ Revert (Press 'R') ]{/}`;
    } else if (this.activeRequestTab === 'pre-script') {
      content = ` {bold}{${t.put}-fg}[ Edit Pre-Script (Press 'I' or Click) ]{/}   ` +
                `{bold}{${t.get}-fg}[ Save .bru (Press 'S') ]{/}   ` +
                `{bold}{${t.delete}-fg}[ Revert (Press 'R') ]{/}\n\n` +
                (req.preRequestScript || `{${t.subtext}-fg}No pre-request script.{/}`);
    } else if (this.activeRequestTab === 'post-script') {
      content = ` {bold}{${t.put}-fg}[ Edit Post-Script (Press 'I' or Click) ]{/}   ` +
                `{bold}{${t.get}-fg}[ Save .bru (Press 'S') ]{/}   ` +
                `{bold}{${t.delete}-fg}[ Revert (Press 'R') ]{/}\n\n` +
                (req.postResponseScript || `{${t.subtext}-fg}No post-response script.{/}`);
    } else if (this.activeRequestTab === 'docs') {
      content = ` {bold}{${t.put}-fg}[ Edit Docs (Press 'I' or Click) ]{/}   ` +
                `{bold}{${t.get}-fg}[ Save .bru (Press 'S') ]{/}   ` +
                `{bold}{${t.delete}-fg}[ Revert (Press 'R') ]{/}\n\n` +
                Interpolator.highlightVariables(req.docs || `{${t.subtext}-fg}No documentation available.{/}`, this.collection, t.accent, t.delete);
    }

    this.requestContentBox.setContent(content);
    this.screen.render();
  }

  private updateResponseView() {
    const t = this.getTheme();

    if (!this.lastResponse) {
      this.responseHeaderBox.setContent(`{${t.subtext}-fg} No response yet. Press {bold}[Space]{/} or {bold}[Enter]{/} to send.{/}`);
      this.responseTabsBox.setContent('');
      this.responseContentBox.setContent('');
      this.screen.render();
      return;
    }

    const resp = this.lastResponse;
    const statusColor = resp.status >= 200 && resp.status < 300
      ? t.statusSuccess
      : resp.status >= 400
        ? t.statusError
        : t.statusWarn;

    const sizeFormatted = resp.sizeBytes > 1024
      ? `${(resp.sizeBytes / 1024).toFixed(1)} KB`
      : `${resp.sizeBytes} B`;

    this.responseHeaderBox.setContent(
      ` {bold}{${statusColor}-fg}${resp.status} ${resp.statusText}{/}  ` +
      `{${t.subtext}-fg}Time:{/} {bold}${resp.durationMs} ms{/}  ` +
      `{${t.subtext}-fg}Size:{/} {bold}${sizeFormatted}{/}`
    );

    const tabs: Array<{ id: 'body' | 'headers' | 'console'; label: string }> = [
      { id: 'body', label: 'Response Body' },
      { id: 'headers', label: `Headers (${Object.keys(resp.headers).length})` },
      { id: 'console', label: `Console (${this.scriptRunner.logs.length})` }
    ];

    this.responseTabRanges = [];
    let respColOffset = 0;
    const tabStr = tabs.map(tab => {
      const visibleLen = tab.label.length + 4;
      this.responseTabRanges.push({
        id: tab.id,
        start: respColOffset,
        end: respColOffset + visibleLen - 1
      });
      respColOffset += visibleLen + 1;
      if (tab.id === this.activeResponseTab) {
        return `{bold}{${t.accent}-fg}[ ${tab.label} ]{/}`;
      }
      return `{${t.subtext}-fg}  ${tab.label}  {/}`;
    }).join(' ');

    this.responseTabsBox.setContent(tabStr);

    let content = '';
    if (this.activeResponseTab === 'body') {
      if (resp.jsonBody) {
        content = this.highlightJson(resp.jsonBody);
      } else {
        content = resp.body;
      }
    } else if (this.activeResponseTab === 'headers') {
      content = Object.entries(resp.headers)
        .map(([k, v]) => `{bold}{#89b4fa-fg}${k}{/}: {#cdd6f4-fg}${v}{/}`)
        .join('\n');
    } else if (this.activeResponseTab === 'console') {
      if (this.scriptRunner.logs.length === 0) {
        content = '{#6c7086-fg}No console logs recorded.{/}';
      } else {
        content = this.scriptRunner.logs.map(l => {
          let color = '#cdd6f4';
          if (l.type === 'error') color = t.statusError;
          else if (l.type === 'warn') color = t.statusWarn;
          else if (l.type === 'info') color = t.statusInfo;
          return `[{#6c7086-fg}${l.timestamp.toTimeString().split(' ')[0]}{/}] {${color}-fg}${l.message}{/}`;
        }).join('\n');
      }
    }

    this.responseContentBox.setContent(content);
    this.screen.render();
  }

  private highlightJson(obj: any): string {
    const t = this.getTheme();
    const str = JSON.stringify(obj, null, 2);
    return str
      .replace(/"(.*?)":/g, `{bold}{${t.put}-fg}"$1"{/}:`)
      .replace(/: "(.*?)"/g, `: {${t.get}-fg}"$1"{/}`)
      .replace(/: (\d+)/g, `: {${t.post}-fg}$1{/}`)
      .replace(/: (true|false)/g, `: {${t.accent}-fg}$1{/}`)
      .replace(/: (null)/g, `: {${t.delete}-fg}$1{/}`);
  }

  private updateStatusBar(msg: string) {
    const t = this.getTheme();
    const shortcuts =
      `{bold}[Space]{/} Send  {bold}[U]{/} URL  {bold}[I]{/} Edit Tab  ` +
      `{bold}[S]{/} Save  {bold}[R]{/} Revert  {bold}[E]{/} Env  {bold}[C]{/} Coll  {bold}[1-7]{/} Tabs  {bold}[q]{/} Quit`;

    this.statusBar.setContent(
      ` ${shortcuts}   ` +
      `{right}{bold}{${t.accent}-fg}${msg}{/} `
    );
    this.screen.render();
  }

  private markRequestModified(req: BrunoRequest) {
    this.modifiedRequests.add(req.id);
    this.renderSidebar();
    this.updateRequestView();
  }

  private bindEvents() {
    // 1. Sidebar keyboard Enter -> toggle folder or select request
    this.sidebarList.key(['enter'], () => {
      if (this.isModalOpen()) return;
      const idx = (this.sidebarList as any).selected;
      this.handleTreeSelection(idx);
    });

    // Sidebar Up/Down navigation -> preview request immediately
    this.sidebarList.on('select item', (_item: any, index: number) => {
      if (this.isUpdatingSidebar) return;
      this.selectedTreeIndex = index;
      const target = this.visibleTreeItems[index];
      if (target && target.type === 'request') {
        this.selectedRequest = target.request;
        this.updateRequestView();
      }
    });

    // 2. Mouse click on any item in sidebar -> single-click opens API or toggles folder
    this.sidebarList.on('element click', (el: any) => {
      if (this.isModalOpen()) return;
      const items = (this.sidebarList as any).items as any[];
      const idx = items ? items.indexOf(el) : -1;
      if (idx >= 0) {
        this.selectedTreeIndex = idx;
        this.handleTreeSelection(idx);
      }
    });

    // 3. Mouse click on Header -> Open Collection, Env, Auth, or Theme based on click position
    this.headerBox.on('click', (data: any) => {
      if (this.isModalOpen()) return;
      const pos = (this.headerBox as any).lpos;
      if (!pos || !data || typeof data.x !== 'number') {
        this.openCollectionModal();
        return;
      }
      const relX = data.x - (pos.xi + 1);
      const relY = data.y - (pos.yi + 1);
      const width = (pos.xl - pos.xi) - 2;

      if (relY === 0 && relX >= width - 35) {
        const newTheme = this.themeManager.cycle();
        this.applyTheme();
        this.renderSidebar();
        this.updateRequestView();
        this.updateResponseView();
        this.updateStatusBar(`Theme: ${newTheme.name}`);
        return;
      }

      if (relY === 1) {
        const collPrefix = 12 + 16 + this.collection.name.length + 2;
        const envName = this.collection.activeEnvironment || 'None';
        const envStart = collPrefix + 3;
        const envEnd = envStart + 9 + envName.length + 2;

        if (relX >= envStart && relX <= envEnd) {
          this.openEnvironmentModal();
          return;
        }
        if (relX > envEnd) {
          this.openAuthManagerModal();
          return;
        }
      }

      this.openCollectionModal();
    });

    // 3b. Mouse click on Request Header Box -> Method, URL, [Edit URL], [Edit], [Save], [Revert]
    this.requestHeaderBox.on('click', (data: any) => {
      if (this.isModalOpen() || !this.selectedRequest) return;
      const pos = (this.requestHeaderBox as any).lpos;
      if (!pos || !data || typeof data.x !== 'number') return;

      const relX = data.x - pos.xi;
      const relY = data.y - pos.yi;
      const width = pos.xl - pos.xi;

      if (relY === 0) {
        const methodBtnLen = this.selectedRequest.method.length + 5;
        if (relX <= methodBtnLen) {
          this.cycleActiveRequestMethod();
          return;
        }
        // Right-aligned buttons: "[Edit URL (U)] [Edit (I)] [Save (S)] [Revert (R)]" (49 chars)
        const rightStart = width - 49;
        if (relX >= rightStart) {
          const offset = relX - rightStart;
          if (offset < 15) {
            this.editActiveRequestUrl();
          } else if (offset < 26) {
            this.editActiveTab();
          } else if (offset < 37) {
            this.saveActiveRequest();
          } else {
            this.revertActiveRequest();
          }
          return;
        }
        this.editActiveRequestUrl();
      } else if (relY === 1) {
        this.editActiveRequestUrl();
      }
    });

    // 4. Mouse click on Request Tabs -> Select exact clicked tab
    this.requestTabsBox.on('click', (data: any) => {
      if (this.isModalOpen()) return;
      const pos = (this.requestTabsBox as any).lpos;
      if (pos && data && typeof data.x === 'number') {
        const relX = data.x - pos.xi;
        const hit = this.requestTabRanges.find(r => relX >= r.start && relX <= r.end);
        if (hit) {
          this.activeRequestTab = hit.id;
          this.updateRequestView();
          return;
        }
      }
    });

    // 5. Mouse click on Response Tabs -> Select exact clicked tab
    this.responseTabsBox.on('click', (data: any) => {
      if (this.isModalOpen()) return;
      const pos = (this.responseTabsBox as any).lpos;
      if (pos && data && typeof data.x === 'number') {
        const relX = data.x - pos.xi;
        const hit = this.responseTabRanges.find(r => relX >= r.start && relX <= r.end);
        if (hit) {
          this.activeResponseTab = hit.id;
          this.updateResponseView();
          return;
        }
      }
    });

    // 6. Global Hotkeys
    this.screen.key(['C-c'], () => {
      return process.exit(0);
    });

    this.screen.key(['q'], () => {
      if (this.isModalOpen()) return;
      return process.exit(0);
    });

    // Space key: Send request
    this.screen.key(['space'], async () => {
      if (this.isModalOpen()) return;
      await this.sendActiveRequest();
    });

    // Open Collection Picker
    this.screen.key(['c', 'C'], () => {
      if (this.isModalOpen()) return;
      this.openCollectionModal();
    });

    // OAuth2 Trigger
    this.screen.key(['o', 'O'], async () => {
      if (this.isModalOpen()) return;
      await this.triggerOAuth2();
    });

    // Cycle Theme
    this.screen.key(['t', 'T'], () => {
      if (this.isModalOpen()) return;
      const newTheme = this.themeManager.cycle();
      this.applyTheme();
      this.renderSidebar();
      this.updateRequestView();
      this.updateResponseView();
      this.updateStatusBar(`Theme: ${newTheme.name}`);
    });

    // Environment Modal
    this.screen.key(['e', 'E'], () => {
      if (this.isModalOpen()) return;
      this.openEnvironmentModal();
    });

    // Edit URL [U]
    this.screen.key(['u', 'U'], () => {
      if (this.isModalOpen()) return;
      this.editActiveRequestUrl();
    });

    // Edit active tab content [I]
    this.screen.key(['i', 'I'], () => {
      if (this.isModalOpen()) return;
      this.editActiveTab();
    });

    // Number keys for request tabs
    this.screen.key(['1'], () => { if (!this.isModalOpen()) { this.activeRequestTab = 'headers'; this.updateRequestView(); } });
    this.screen.key(['2'], () => { if (!this.isModalOpen()) { this.activeRequestTab = 'params'; this.updateRequestView(); } });
    this.screen.key(['3'], () => { if (!this.isModalOpen()) { this.activeRequestTab = 'body'; this.updateRequestView(); } });
    this.screen.key(['4'], () => { if (!this.isModalOpen()) { this.activeRequestTab = 'auth'; this.updateRequestView(); } });
    this.screen.key(['5'], () => { if (!this.isModalOpen()) { this.activeRequestTab = 'pre-script'; this.updateRequestView(); } });
    this.screen.key(['6'], () => { if (!this.isModalOpen()) { this.activeRequestTab = 'post-script'; this.updateRequestView(); } });
    this.screen.key(['7'], () => { if (!this.isModalOpen()) { this.activeRequestTab = 'docs'; this.updateRequestView(); } });

    // Tab key switches focus
    this.screen.key(['tab'], () => {
      if (this.isModalOpen()) return;
      if (this.screen.focused === this.sidebarList) {
        this.responseContentBox.focus();
      } else if (this.screen.focused === this.responseContentBox) {
        this.requestContentBox.focus();
      } else {
        this.sidebarList.focus();
      }
      this.screen.render();
    });

    // Mouse click on Request Content Box (Handles interactive actions across all tabs)
    this.requestContentBox.on('click', async (data: any) => {
      if (this.isModalOpen() || !this.selectedRequest) return;
      const req = this.selectedRequest;

      const pos = (this.requestContentBox as any).lpos;
      if (!pos || !data || typeof data.x !== 'number' || typeof data.y !== 'number') return;

      const relX = data.x - pos.xi;
      const relY = data.y - pos.yi + ((this.requestContentBox as any).childBase || 0);
      const lines = (this.requestContentBox.getContent() || '').split('\n');
      const clickedLine = lines[relY] || '';

      // Common Save / Revert buttons on action lines
      if (clickedLine.includes('Save .bru') || clickedLine.includes('Save (Press')) {
        if (clickedLine.includes('Revert') && relX > 62) {
          this.revertActiveRequest();
          return;
        }
        if (clickedLine.includes('Add Header') || clickedLine.includes('Add Param') || clickedLine.includes('Edit ')) {
          if (relX < 38) {
            this.editActiveTab();
          } else if (relX < 64) {
            this.saveActiveRequest();
          } else {
            this.revertActiveRequest();
          }
          return;
        }
      }

      if (this.activeRequestTab === 'headers') {
        if (relY >= 0 && relY < req.headers.length) {
          const h = req.headers[relY];
          const cleanLine = clickedLine.replace(/\{[^}]*\}/g, '');
          if (relX >= cleanLine.length - 6) {
            req.headers.splice(relY, 1);
            this.markRequestModified(req);
            this.updateStatusBar(`Removed header: ${h.name}`);
          } else {
            this.openSingleLinePrompt(`Edit Header #${relY + 1} (Name: Value)`, `${h.name}: ${h.value}`, (val) => {
              const sep = val.indexOf(':');
              if (sep > 0) {
                h.name = val.slice(0, sep).trim();
                h.value = val.slice(sep + 1).trim();
                this.markRequestModified(req);
                this.updateStatusBar(`Updated header: ${h.name}`);
              }
            });
          }
        }
        return;
      }

      if (this.activeRequestTab === 'params') {
        if (relY >= 0 && relY < req.params.length) {
          const p = req.params[relY];
          const cleanLine = clickedLine.replace(/\{[^}]*\}/g, '');
          if (relX >= cleanLine.length - 6) {
            req.params.splice(relY, 1);
            this.markRequestModified(req);
            this.updateStatusBar(`Removed param: ${p.name}`);
          } else {
            this.openSingleLinePrompt(`Edit Param #${relY + 1} (key=value)`, `${p.name}=${p.value}`, (val) => {
              const sep = val.indexOf('=');
              if (sep > 0) {
                p.name = val.slice(0, sep).trim();
                p.value = val.slice(sep + 1).trim();
                this.markRequestModified(req);
                this.updateStatusBar(`Updated param: ${p.name}`);
              }
            });
          }
        }
        return;
      }

      if (this.activeRequestTab === 'body') {
        if (relY === 0) {
          const hitBt = this.bodyTypeRanges.find(r => relX >= r.start && relX <= r.end);
          if (hitBt) {
            req.bodyType = hitBt.id;
            this.markRequestModified(req);
            this.updateStatusBar(`Body type set to: ${hitBt.id}`);
          }
          return;
        }
        this.editActiveTab();
        return;
      }

      if (this.activeRequestTab === 'pre-script' || this.activeRequestTab === 'post-script' || this.activeRequestTab === 'docs') {
        this.editActiveTab();
        return;
      }

      if (this.activeRequestTab === 'auth') {
        if (relY === 0) {
          const hitMode = this.authModeRanges.find(r => relX >= r.start && relX <= r.end);
          if (hitMode) {
            req.authType = hitMode.id;
            this.markRequestModified(req);
            this.updateHeader();
            this.updateStatusBar(`Auth mode switched to: [${hitMode.id.toUpperCase()}]`);
            return;
          }
        }

        if (clickedLine.includes('GET ACCESS TOKEN')) {
          await this.triggerOAuth2();
          return;
        }
        if (clickedLine.includes('Cycle Mode')) {
          if (relX >= 32 && relX <= 58) {
            const modes: Array<BrunoRequest['authType']> = ['inherit', 'none', 'bearer', 'basic', 'oauth2'];
            const curIdx = modes.indexOf(req.authType);
            req.authType = modes[(curIdx + 1) % modes.length];
            this.markRequestModified(req);
            this.updateHeader();
            this.updateStatusBar(`Auth mode switched to: [${req.authType.toUpperCase()}]`);
            return;
          }
          if (relX > 58 && relX <= 78) {
            this.saveActiveRequest();
            return;
          }
          if (relX > 78) {
            this.revertActiveRequest();
            return;
          }
        }

        this.openAuthManagerModal();
      }
    });

    // Hotkey [A]: Auth Manager Dialog
    this.screen.key(['a', 'A'], () => {
      if (this.isModalOpen()) return;
      this.openAuthManagerModal();
    });

    // Hotkey [M]: Cycle Auth Mode (if on Auth Tab) or HTTP Method
    this.screen.key(['m', 'M'], () => {
      if (this.isModalOpen() || !this.selectedRequest) return;
      if (this.activeRequestTab === 'auth') {
        const modes: Array<BrunoRequest['authType']> = ['inherit', 'none', 'bearer', 'basic', 'oauth2'];
        const curIdx = modes.indexOf(this.selectedRequest.authType);
        this.selectedRequest.authType = modes[(curIdx + 1) % modes.length];
        this.markRequestModified(this.selectedRequest);
        this.updateHeader();
        this.updateStatusBar(`Auth mode switched to: [${this.selectedRequest.authType.toUpperCase()}]`);
      } else {
        this.cycleActiveRequestMethod();
      }
    });

    // Hotkey [S]: Save entire request to .bru file
    this.screen.key(['s', 'S'], () => {
      if (this.isModalOpen()) return;
      this.saveActiveRequest();
    });

    // Hotkey [R]: Revert request from .bru file on disk
    this.screen.key(['r', 'R'], () => {
      if (this.isModalOpen()) return;
      this.revertActiveRequest();
    });
  }

  private cycleActiveRequestMethod() {
    const req = this.selectedRequest;
    if (!req) return;
    const methods: Array<BrunoRequest['method']> = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];
    const idx = methods.indexOf(req.method);
    req.method = methods[(idx + 1) % methods.length];
    this.markRequestModified(req);
    this.updateStatusBar(`HTTP method changed to: ${req.method}`);
  }

  private editActiveRequestUrl() {
    const req = this.selectedRequest;
    if (!req) return;
    this.openSingleLinePrompt(`Edit URL for [${req.method} ${req.name}]`, req.url, (newUrl) => {
      if (newUrl.trim() && newUrl !== req.url) {
        req.url = newUrl.trim();
        this.markRequestModified(req);
        this.updateStatusBar(`Updated URL (Press [S] to save or [R] to revert)`);
      }
    });
  }

  private editActiveTab() {
    const req = this.selectedRequest;
    if (!req) return;

    if (this.activeRequestTab === 'headers') {
      this.openSingleLinePrompt('Add Header (Format: Header-Name: value)', '', (val) => {
        const sep = val.indexOf(':');
        if (sep > 0) {
          const name = val.slice(0, sep).trim();
          const value = val.slice(sep + 1).trim();
          req.headers.push({ name, value, enabled: true });
          this.markRequestModified(req);
          this.updateStatusBar(`Added header: ${name}`);
        } else {
          this.updateStatusBar('Invalid header format. Use Name: Value');
        }
      });
    } else if (this.activeRequestTab === 'params') {
      this.openSingleLinePrompt('Add Query Param (Format: key=value)', '', (val) => {
        const sep = val.indexOf('=');
        if (sep > 0) {
          const name = val.slice(0, sep).trim();
          const value = val.slice(sep + 1).trim();
          req.params.push({ name, value, enabled: true });
          this.markRequestModified(req);
          this.updateStatusBar(`Added query param: ${name}`);
        } else {
          this.updateStatusBar('Invalid param format. Use key=value');
        }
      });
    } else if (this.activeRequestTab === 'body') {
      if (req.bodyType === 'none') {
        req.bodyType = 'json';
      }
      this.openMultiLineEditor(`Edit Request Body (${req.bodyType.toUpperCase()})`, req.bodyContent || '', (newBody) => {
        req.bodyContent = newBody;
        this.markRequestModified(req);
        this.updateStatusBar(`Body updated (Press [S] to save or [R] to revert)`);
      });
    } else if (this.activeRequestTab === 'auth') {
      this.openAuthManagerModal();
    } else if (this.activeRequestTab === 'pre-script') {
      this.openMultiLineEditor('Edit Pre-Request Script (JS)', req.preRequestScript || '', (newScript) => {
        req.preRequestScript = newScript;
        this.markRequestModified(req);
        this.updateStatusBar(`Pre-request script updated`);
      });
    } else if (this.activeRequestTab === 'post-script') {
      this.openMultiLineEditor('Edit Post-Response Script (JS)', req.postResponseScript || '', (newScript) => {
        req.postResponseScript = newScript;
        this.markRequestModified(req);
        this.updateStatusBar(`Post-response script updated`);
      });
    } else if (this.activeRequestTab === 'docs') {
      this.openMultiLineEditor('Edit Request Documentation (Markdown)', req.docs || '', (newDocs) => {
        req.docs = newDocs;
        this.markRequestModified(req);
        this.updateStatusBar(`Documentation updated`);
      });
    }
  }

  private openSingleLinePrompt(title: string, initialValue: string, onSubmit: (val: string) => void) {
    if (this.isModalOpen()) return;
    const t = this.getTheme();

    this.editorModal = blessed.box({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '70%',
      height: 7,
      border: { type: 'line' },
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.accent }
      },
      tags: true,
      mouse: true
    });

    blessed.box({
      parent: this.editorModal,
      top: 0,
      left: 1,
      right: 1,
      height: 1,
      content: `{bold}{${t.accent}-fg}${title}{/}`,
      style: { bg: t.surface, fg: t.fg },
      tags: true
    });

    const input = blessed.textbox({
      parent: this.editorModal,
      top: 2,
      left: 1,
      right: 1,
      height: 1,
      inputOnFocus: true,
      keys: true,
      mouse: true,
      style: {
        bg: t.bg,
        fg: t.fg,
        focus: { bg: t.bg, fg: t.fg }
      }
    });

    blessed.box({
      parent: this.editorModal,
      top: 4,
      left: 1,
      right: 1,
      height: 1,
      content: `{${t.subtext}-fg}[Enter] Apply   [Esc] Cancel   (Supports {{VAR}} placeholders){/}`,
      style: { bg: t.surface, fg: t.subtext },
      tags: true
    });

    const closePrompt = () => {
      if (this.editorModal) {
        this.editorModal.destroy();
        this.editorModal = undefined;
        this.sidebarList.focus();
        this.screen.render();
      }
    };

    input.setValue(initialValue);

    input.on('submit', (val: string) => {
      closePrompt();
      onSubmit(val);
    });

    input.on('cancel', () => {
      closePrompt();
    });

    input.key(['escape'], () => {
      closePrompt();
    });

    input.focus();
    this.screen.render();
  }

  private openMultiLineEditor(title: string, initialValue: string, onSubmit: (val: string) => void) {
    if (this.isModalOpen()) return;
    const t = this.getTheme();

    this.editorModal = blessed.box({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '80%',
      height: '75%',
      border: { type: 'line' },
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.accent }
      },
      tags: true,
      mouse: true
    });

    blessed.box({
      parent: this.editorModal,
      top: 0,
      left: 1,
      right: 28,
      height: 1,
      content: `{bold}{${t.accent}-fg}${title}{/}`,
      style: { bg: t.surface, fg: t.fg },
      tags: true
    });

    const applyBtn = blessed.box({
      parent: this.editorModal,
      top: 0,
      right: 13,
      width: 13,
      height: 1,
      content: `{bold}{${t.get}-fg}[Apply C-s]{/}`,
      style: { bg: t.surface, fg: t.get },
      tags: true,
      mouse: true
    });

    const cancelBtn = blessed.box({
      parent: this.editorModal,
      top: 0,
      right: 1,
      width: 11,
      height: 1,
      content: `{bold}{${t.delete}-fg}[X Cancel]{/}`,
      style: { bg: t.surface, fg: t.delete },
      tags: true,
      mouse: true
    });

    const textarea = blessed.textarea({
      parent: this.editorModal,
      top: 2,
      left: 1,
      right: 1,
      bottom: 2,
      inputOnFocus: true,
      keys: true,
      mouse: true,
      scrollable: true,
      alwaysScroll: true,
      border: { type: 'line' },
      style: {
        bg: t.bg,
        fg: t.fg,
        border: { fg: t.borderFocus }
      }
    });

    blessed.box({
      parent: this.editorModal,
      bottom: 0,
      left: 1,
      right: 1,
      height: 1,
      content: `{${t.subtext}-fg}[Ctrl+S] or Click [Apply C-s] to confirm changes   [Esc] or Click [X Cancel] to discard{/}`,
      style: { bg: t.surface, fg: t.subtext },
      tags: true
    });

    const closeEditor = () => {
      if (this.editorModal) {
        this.editorModal.destroy();
        this.editorModal = undefined;
        this.sidebarList.focus();
        this.screen.render();
      }
    };

    const finishSave = () => {
      const val = textarea.getValue();
      closeEditor();
      onSubmit(val);
    };

    textarea.setValue(initialValue);

    applyBtn.on('click', () => {
      finishSave();
    });

    cancelBtn.on('click', () => {
      closeEditor();
    });

    textarea.key(['C-s'], () => {
      finishSave();
    });

    textarea.key(['escape'], () => {
      closeEditor();
    });

    textarea.focus();
    this.screen.render();
  }

  private handleTreeSelection(index: number) {
    const item = this.visibleTreeItems[index];
    if (!item) return;

    if (item.type === 'folder') {
      if (this.expandedFolders.has(item.folder.id)) {
        this.expandedFolders.delete(item.folder.id);
      } else {
        this.expandedFolders.add(item.folder.id);
      }
      this.selectedTreeIndex = index;
      this.rebuildTree();
      this.renderSidebar();
    } else {
      this.selectedTreeIndex = index;
      this.selectedRequest = item.request;
      (this.sidebarList as any).select(index);
      this.updateRequestView();
      this.screen.render();
    }
  }

  public isModalOpen(): boolean {
    return !!this.collectionModal ||
           !!this.editorModal ||
           (!!this.envManagerModal && this.envManagerModal.isOpen()) ||
           (!!this.authManagerModal && this.authManagerModal.isOpen());
  }

  private openCollectionModal() {
    if (this.isModalOpen()) return;

    const t = this.getTheme();
    const discovered = CollectionParser.discoverCollections(this.collectionPath);

    this.collectionModal = blessed.box({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '65%',
      height: '60%',
      border: { type: 'line' },
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.accent }
      },
      tags: true,
      mouse: true
    });

    blessed.box({
      parent: this.collectionModal,
      top: 0,
      left: 1,
      right: 12,
      height: 2,
      content: `{bold}{${t.accent}-fg}Select a Bruno Collection{/} {${t.subtext}-fg}(Enter/Click to open, Esc/q to cancel){/}`,
      tags: true
    });

    const closeBtn = blessed.box({
      parent: this.collectionModal,
      top: 0,
      right: 1,
      width: 11,
      height: 1,
      content: `{bold}{${t.delete}-fg}[X Close]{/}`,
      tags: true
    });

    const collList = blessed.list({
      parent: this.collectionModal,
      top: 2,
      left: 1,
      right: 1,
      bottom: 1,
      keys: true,
      vi: true,
      mouse: true,
      tags: true,
      style: {
        bg: t.surface,
        fg: t.fg,
        selected: { bg: t.selection, fg: t.selectionFg, bold: true },
        item: { bg: t.surface, fg: t.fg }
      },
      items: discovered.map(c => {
        const isCurrent = c.path === this.collectionPath;
        const star = isCurrent ? `{${t.get}-fg}* (current){/} ` : '  ';
        return `${star}{bold}${c.name}{/} {${t.subtext}-fg}${c.path}{/}`;
      })
    });

    let collKeyHandler: ((ch: any, key: any) => void) | undefined;

    const closeModal = () => {
      if (collKeyHandler) {
        this.screen.removeListener('keypress', collKeyHandler);
        collKeyHandler = undefined;
      }
      if (this.collectionModal) {
        this.collectionModal.destroy();
        this.collectionModal = undefined;
        this.sidebarList.focus();
        this.screen.render();
      }
    };

    closeBtn.on('click', () => {
      closeModal();
    });

    collList.on('select', (_item: any, index: number) => {
      const selected = discovered[index];
      if (selected && selected.path !== this.collectionPath) {
        this.loadCollection(selected.path);
        this.updateHeader();
        this.renderSidebar();
        this.updateRequestView();
        this.updateResponseView();
        this.updateStatusBar(`Switched collection to: ${selected.name}`);
      }
      closeModal();
    });

    collKeyHandler = (ch: any, key: any) => {
      if (!this.collectionModal) return;
      if (!key) return;
      if (key.name === 'escape' || ch === 'q' || ch === 'Q') {
        closeModal();
      }
    };

    this.screen.on('keypress', collKeyHandler);

    collList.focus();
    this.screen.render();
  }

  private openEnvironmentModal() {
    if (!this.envManagerModal) {
      this.envManagerModal = new EnvironmentManagerModal(
        this.screen,
        this.collection,
        this.themeManager,
        () => {
          this.updateHeader();
          this.updateRequestView();
          this.updateResponseView();
        },
        () => {
          this.sidebarList.focus();
          this.screen.render();
        }
      );
    }
    this.envManagerModal.show();
  }

  private async sendActiveRequest() {
    if (this.isRunning) return;
    const req = this.selectedRequest;
    if (!req) {
      this.updateStatusBar('No request selected');
      return;
    }

    this.isRunning = true;
    this.scriptRunner.clearLogs();
    this.updateStatusBar(`Executing ${req.method} ${req.name}...`);

    try {
      await this.scriptRunner.runPreRequestScripts(req);
      this.updateHeader();

      const resp = await HttpClient.execute(req, this.collection);
      this.lastResponse = resp;

      await this.scriptRunner.runPostResponseScript(req, resp);
      this.updateHeader();

      this.updateResponseView();
      this.updateStatusBar(`Done: ${resp.status} ${resp.statusText} (${resp.durationMs}ms)`);
    } catch (err: any) {
      this.updateStatusBar(`Error: ${err.message}`);
    } finally {
      this.isRunning = false;
    }
  }

  private openAuthManagerModal() {
    const req = this.selectedRequest;
    if (!req) {
      this.updateStatusBar('No request selected to configure auth.');
      return;
    }
    if (this.isModalOpen()) return;

    this.authManagerModal = new AuthManagerModal(
      this.screen,
      this.collection,
      req,
      this.themeManager,
      () => {
        if (req) {
          this.modifiedRequests.add(req.id);
          this.renderSidebar();
        }
        this.updateHeader();
        this.updateRequestView();
        this.updateResponseView();
      },
      () => {
        this.sidebarList.focus();
        this.screen.render();
      }
    );
    this.authManagerModal.show();
  }

  private saveActiveRequest() {
    const req = this.selectedRequest;
    if (!req) return;
    try {
      const filePath = req.filePath;
      if (!fs.existsSync(filePath)) {
        throw new Error(`Request file not found: ${filePath}`);
      }

      const raw = fs.readFileSync(filePath, 'utf8');
      const json = bruToJsonV2(raw) as any;

      if (!json.meta) {
        json.meta = { name: req.name, type: 'http', seq: req.seq || 1 };
      }

      if (!json.http) json.http = {};
      json.http.method = req.method.toLowerCase();
      json.http.url = req.url;
      json.http.body = req.bodyType;
      json.http.auth = req.authType;

      json.headers = req.headers.map(h => ({
        name: h.name,
        value: h.value,
        enabled: h.enabled !== false
      }));

      json.params = req.params.map(p => ({
        name: p.name,
        value: p.value,
        enabled: p.enabled !== false,
        type: 'query'
      }));

      if (!json.body) json.body = {};
      if (req.bodyType === 'json') {
        json.body.json = req.bodyContent;
      } else if (req.bodyType === 'text') {
        json.body.text = req.bodyContent;
      } else if (req.bodyType === 'xml') {
        json.body.xml = req.bodyContent;
      } else if (req.bodyType === 'formUrlEncoded') {
        json.body.formUrlEncoded = req.bodyContent
          .split(/\r?\n/)
          .map(line => line.trim())
          .filter(line => line.length > 0)
          .map(line => {
            const idx = line.indexOf('=');
            if (idx === -1) return { name: line, value: '', enabled: true };
            return {
              name: line.substring(0, idx).trim(),
              value: line.substring(idx + 1).trim(),
              enabled: true
            };
          });
      }

      if (!json.auth) json.auth = {};
      json.auth.mode = req.authType;

      if (req.authType === 'bearer') {
        json.auth.bearer = {
          token: req.authBearerToken || '{{access_token}}'
        };
      } else if (req.authType === 'basic') {
        json.auth.basic = {
          username: req.authBasicUser || '',
          password: req.authBasicPassword || ''
        };
      } else if (req.authType === 'oauth2' && req.authOAuth2) {
        const o = req.authOAuth2;
        json.auth.oauth2 = {
          grantType: o.grantType,
          accessTokenUrl: o.tokenUrl,
          authorizationUrl: o.authUrl || '',
          callbackUrl: o.redirectUri || '',
          clientId: o.clientId,
          clientSecret: o.clientSecret || '',
          username: o.username || '',
          password: o.password || '',
          scope: o.scope || 'openid',
          pkce: o.pkce !== false
        };
      }

      if (req.preRequestScript || req.postResponseScript || json.script) {
        if (!json.script) json.script = {};
        if (req.preRequestScript !== undefined) {
          json.script.req = req.preRequestScript;
        }
        if (req.postResponseScript !== undefined) {
          json.script.res = req.postResponseScript;
        }
      }

      if (req.docs !== undefined) {
        json.docs = req.docs;
      }

      const updated = jsonToBruV2(json);
      fs.writeFileSync(filePath, updated, 'utf8');
      this.modifiedRequests.delete(req.id);
      this.renderSidebar();
      this.updateRequestView();
      this.updateStatusBar(`Saved changes to ${path.basename(filePath)}`);
    } catch (err: any) {
      this.updateStatusBar(`Save error: ${err.message}`);
    }
  }

  private revertActiveRequest() {
    const req = this.selectedRequest;
    if (!req) return;
    try {
      const fresh = this.parser.parseRequestFile(req.filePath, req.relativePath);
      if (!fresh) {
        throw new Error(`Failed to reload ${path.basename(req.filePath)}`);
      }
      req.method = fresh.method;
      req.url = fresh.url;
      req.headers = fresh.headers;
      req.params = fresh.params;
      req.bodyType = fresh.bodyType;
      req.bodyContent = fresh.bodyContent;
      req.authType = fresh.authType;
      req.authBearerToken = fresh.authBearerToken;
      req.authBasicUser = fresh.authBasicUser;
      req.authBasicPassword = fresh.authBasicPassword;
      req.authOAuth2 = fresh.authOAuth2;
      req.preRequestScript = fresh.preRequestScript;
      req.postResponseScript = fresh.postResponseScript;
      req.docs = fresh.docs;

      this.modifiedRequests.delete(req.id);
      this.renderSidebar();
      this.updateRequestView();
      this.updateStatusBar(`Reverted ${req.name} from ${path.basename(req.filePath)}`);
    } catch (err: any) {
      this.updateStatusBar(`Revert error: ${err.message}`);
    }
  }

  private async triggerOAuth2() {
    const req = this.selectedRequest;
    let oauthConfig: OAuth2Config | undefined = req?.authOAuth2;

    if (!oauthConfig && this.collection.collectionAuthOAuth2) {
      oauthConfig = this.collection.collectionAuthOAuth2;
    }

    if (!oauthConfig && this.collection.activeEnvironment) {
      const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
      if (env) {
        const host = env.variables['APIGEE_HOST'] || 'https://apitest.tim.it';
        const clientId = env.variables['KC_CLIENT_ID'];
        const clientSecret = env.variables['KC_CLIENT_SECRET'];
        const username = env.variables['KC_USERNAME'];
        const password = env.variables['KC_PASSWORD'];

        if (clientId) {
          oauthConfig = {
            grantType: username ? 'password' : 'authorization_code',
            tokenUrl: `${host}/auth/oauth/v2/token`,
            authUrl: `${host}/auth/oauth/v2/authorize`,
            redirectUri: 'http://127.0.0.1:3000/callback',
            clientId,
            clientSecret,
            username,
            password,
            scope: 'openid',
            pkce: true
          };
        }
      }
    }

    // Check if collection has an auth sub-request like "_Auth/Get Token - APP"
    if (!oauthConfig) {
      const tokenReq = this.parser.requestMap.get('_Auth/Get Token - APP') ||
                       this.parser.requestMap.get('_Auth/Get Token') ||
                       this.parser.requestMap.get('Get Token');
      if (tokenReq) {
        this.updateStatusBar(`Running auth request: ${tokenReq.name}...`);
        try {
          const resp = await HttpClient.execute(tokenReq, this.collection);
          await this.scriptRunner.runPostResponseScript(tokenReq, resp);
          this.updateHeader();
          this.updateRequestView();
          this.updateStatusBar(`Auth request completed: HTTP ${resp.status}`);
          return;
        } catch (err: any) {
          this.updateStatusBar(`Auth request error: ${err.message}`);
          return;
        }
      }
    }

    if (!oauthConfig) {
      this.openAuthManagerModal();
      return;
    }

    this.updateStatusBar(`Starting OAuth2 (${oauthConfig.grantType.toUpperCase()})...`);
    try {
      const result = await OAuth2Handler.authenticate(oauthConfig, this.collection, (msg) => {
        this.updateStatusBar(msg);
      });
      this.updateHeader();
      this.updateRequestView();
      const expStr = result.expiresIn ? ` (Expires in ${result.expiresIn}s)` : '';
      this.updateStatusBar(`OAuth2 success! Token saved: ${result.accessToken.slice(0, 15)}...${expStr}`);
    } catch (err: any) {
      this.updateStatusBar(`OAuth2 error: ${err.message}`);
    }
  }
}
