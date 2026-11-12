import blessed from 'neo-blessed';
import * as path from 'node:path';
import * as fs from 'node:fs';
import { bruToJsonV2, jsonToBruV2, collectionBruToJson, jsonToCollectionBru } from '@usebruno/lang';
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
  private inlineTarget: 'none' | 'url' | 'content' | 'coll-headers' = 'none';
  private inlineText: string = '';
  private inlineCursor: number = 0;

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
      smartCSR: false,
      terminal: 'xterm-256color',
      fullUnicode: true,
      title: `brui - ${this.collection.name}`,
      mouse: true,
      sendFocus: true,
      useBCE: false,
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

    // 1. Top Header (Height 5 -> 3 inner lines for 6x9 pixel-art Bruno dog + 2 text lines)
    this.headerBox = blessed.box({
      parent: this.screen,
      top: 0,
      left: 0,
      width: '100%',
      height: 5,
      tags: true,
      border: { type: 'line' },
      mouse: true,
      autoFocus: false
    } as any);

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
      height: '55%-5',
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
      mouse: true,
      autoFocus: false
    } as any);

    this.requestTabsBox = blessed.box({
      parent: this.requestPane,
      top: 2,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      mouse: true,
      autoFocus: false
    } as any);

    this.requestContentBox = blessed.box({
      parent: this.requestPane,
      top: 3,
      left: 1,
      right: 1,
      bottom: 0,
      tags: true,
      mouse: true,
      autoFocus: false,
      scrollable: true,
      alwaysScroll: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.bg },
        style: { bg: theme.selection }
      }
    } as any);

    // 4. Response Pane (Bottom Right)
    this.responsePane = blessed.box({
      parent: this.screen,
      top: '55%',
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
      mouse: true,
      autoFocus: false
    } as any);

    this.responseTabsBox = blessed.box({
      parent: this.responsePane,
      top: 2,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      mouse: true,
      autoFocus: false
    } as any);

    this.responseContentBox = blessed.box({
      parent: this.responsePane,
      top: 3,
      left: 1,
      right: 1,
      bottom: 0,
      tags: true,
      mouse: true,
      autoFocus: false,
      scrollable: true,
      alwaysScroll: true,
      scrollbar: {
        ch: ' ',
        track: { bg: theme.bg },
        style: { bg: theme.selection }
      }
    } as any);

    // 5. Status Bar (Bottom)
    this.statusBar = blessed.box({
      parent: this.screen,
      bottom: 0,
      left: 0,
      width: '100%',
      height: 2,
      tags: true,
      mouse: true,
      autoFocus: false
    } as any);

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

    // 6x9 half-block pixel-art sprite of Bruno the Golden Retriever (3 rows x 9 cols):
    // E = floppy ears (#c97a2b), Y = gold fur (#f4aa42), K = eyes/nose (#11111b), R = tongue (#ea5a47)
    const E = '#c97a2b';
    const Y = '#f4aa42';
    const K = '#11111b';
    const R = '#ea5a47';

    const dog1 = `{${E}-fg}▄█{/}{${Y}-fg}█████{/}{${E}-fg}█▄{/}`;
    const dog2 = `{${E}-fg}▀{/}{${E}-fg}{${Y}-bg}▀{/}{${K}-fg}{${Y}-bg}▀{/}{${t.bg}-bg}{${Y}-fg}█{/}{${Y}-fg}{${K}-bg}▀{/}{${t.bg}-bg}{${Y}-fg}█{/}{${K}-fg}{${Y}-bg}▀{/}{${E}-fg}{${Y}-bg}▀{/}{${t.bg}-bg}{${E}-fg}▀{/}`;
    const dog3 = `  {${Y}-fg}▀▀{/}{${R}-fg}█{/}{${Y}-fg}▀▀{/}  `;

    const line1 = ` ${dog1}  {bold}{#f4aa42-fg}BRUI{/} {${t.subtext}-fg}- Bruno Terminal Client{/}`;
    const line2 = ` ${dog2}  {${t.subtext}-fg}Collection [C]:{/} {bold}{${t.put}-fg}[${this.collection.name}]{/}   ` +
                  `{${t.subtext}-fg}Env [E]:{/} {bold}{${t.options}-fg}[${envName}]{/}`;
    const line3 = ` ${dog3}`;

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
    const escapeTags = (s: string) => s.replace(/\{/g, '{open}').replace(/\}/g, '{close}');

    const renderSingleLineWithCursor = (raw: string, isEditing: boolean, cur: number): string => {
      if (!isEditing) {
        return Interpolator.highlightVariables(raw, this.collection, t.accent, t.delete);
      }
      const c = Math.max(0, Math.min(raw.length, cur));
      const before = escapeTags(raw.slice(0, c));
      const atChar = c < raw.length ? escapeTags(raw[c]) : ' ';
      const after = c < raw.length ? escapeTags(raw.slice(c + 1)) : '';
      return `${before}{inverse}${atChar}{/inverse}${after}`;
    };

    const urlDisplay = renderSingleLineWithCursor(
      this.inlineTarget === 'url' ? this.inlineText : req.url,
      this.inlineTarget === 'url',
      this.inlineCursor
    );
    const resolvedUrlColored = Interpolator.interpolateWithColor(req.url, this.collection, t.accent, t.delete);

    const headerLine1 =
      ` {bold}{${badgeColor}-fg}[ ${req.method} ]{/}  {bold}${urlDisplay}{/}${modBadge}`;
    const headerLine2 =
      ` {bold}{${t.get}-fg}[Send]{/}  {bold}{${t.options}-fg}[Save .bru (Ctrl+S)]{/}  {bold}{${t.delete}-fg}[Revert]{/}` +
      (req.url.includes('{{') ? `   {${t.subtext}-fg}-> ${resolvedUrlColored}{/}` : '');

    this.requestHeaderBox.setContent(`${headerLine1}\n${headerLine2}`);

    const collHeaders = (this.collection.collectionHeaders || []).filter(h => h.enabled !== false);
    const headersCountLabel = collHeaders.length > 0
      ? `${req.headers.length}+${collHeaders.length}`
      : `${req.headers.length}`;

    const tabs: Array<{ id: 'headers' | 'params' | 'body' | 'auth' | 'pre-script' | 'post-script' | 'docs'; label: string }> = [
      { id: 'headers', label: `Headers (${headersCountLabel})` },
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

    const renderInlineBlock = (
      rawText: string,
      isEditing: boolean,
      cursorOffset: number,
      placeholder: string,
      showLineResolvedPreview: boolean = false
    ): string => {
      if (!isEditing && !rawText) {
        return ` {${t.subtext}-fg}1 │ ${placeholder} (Click here to type directly){/}`;
      }
      const lines = rawText.split('\n');
      let rem = Math.max(0, Math.min(rawText.length, cursorOffset));
      let curRow = 0;
      let curCol = 0;
      for (let i = 0; i < lines.length; i++) {
        if (rem <= lines[i].length) {
          curRow = i;
          curCol = rem;
          break;
        }
        rem -= lines[i].length + 1;
      }

      return lines.map((line, idx) => {
        const prefix = ` {${t.subtext}-fg}${String(idx + 1).padStart(2, ' ')} │ {/}`;
        let lineBody: string;
        if (isEditing && idx === curRow) {
          const before = escapeTags(line.slice(0, curCol));
          const atChar = curCol < line.length ? escapeTags(line[curCol]) : ' ';
          const after = curCol < line.length ? escapeTags(line.slice(curCol + 1)) : '';
          const emptyHint = line.length === 0 && lines.length === 1
            ? `  {${t.subtext}-fg}(type ${escapeTags(placeholder.replace(/\n/g, ' '))}){/}`
            : '';
          lineBody = `${before}{inverse}${atChar}{/inverse}${after}${emptyHint}`;
        } else {
          lineBody = Interpolator.highlightVariables(line, this.collection, t.accent, t.delete);
        }
        const resPreview = showLineResolvedPreview && line.includes('{{')
          ? `  {${t.subtext}-fg}(-> ${Interpolator.interpolateWithColor(line, this.collection, t.accent, t.delete)}{${t.subtext}-fg}){/}`
          : '';
        return `${prefix}${lineBody}${resPreview}`;
      }).join('\n');
    };

    const isEditingContent = this.inlineTarget === 'content';
    const isEditingCollHeaders = this.inlineTarget === 'coll-headers';
    let content = '';
    if (this.activeRequestTab === 'headers') {
      const rawHeaders = isEditingContent
        ? this.inlineText
        : req.headers.map(h => `${h.name}: ${h.value}`).join('\n');
      content = renderInlineBlock(
        rawHeaders,
        isEditingContent,
        this.inlineCursor,
        'Header-Name: value',
        true
      );

      const rawCollHeaders = isEditingCollHeaders
        ? this.inlineText
        : (this.collection.collectionHeaders || []).filter(h => h.enabled !== false).map(h => `${h.name}: ${h.value}`).join('\n');

      content += `\n\n {bold}{${t.accent}-fg}--- Inherited from collection.bru (${collHeaders.length}) ---{/} {${t.subtext}-fg}[Click here to edit collection headers]{/}\n`;
      content += renderInlineBlock(
        rawCollHeaders,
        isEditingCollHeaders,
        isEditingCollHeaders ? this.inlineCursor : 0,
        'Header-Name: value',
        true
      );
    } else if (this.activeRequestTab === 'params') {
      const rawParams = isEditingContent
        ? this.inlineText
        : req.params.map(p => `${p.name}=${p.value}`).join('\n');
      content = renderInlineBlock(
        rawParams,
        isEditingContent,
        this.inlineCursor,
        'key=value',
        true
      );
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
      const rawBody = isEditingContent ? this.inlineText : (req.bodyContent || '');
      content += renderInlineBlock(
        rawBody,
        isEditingContent,
        this.inlineCursor,
        req.bodyType === 'formUrlEncoded' ? 'key=value' : '{\n  "key": "value"\n}',
        false
      );
      if (rawBody.includes('{{')) {
        content += `\n\n {${t.subtext}-fg}--- Resolved Body Preview ---{/}\n`;
        content += Interpolator.interpolateWithColor(rawBody, this.collection, t.accent, t.delete);
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
      const rawPre = isEditingContent ? this.inlineText : (req.preRequestScript || '');
      content = renderInlineBlock(rawPre, isEditingContent, this.inlineCursor, '// Pre-request JavaScript');
    } else if (this.activeRequestTab === 'post-script') {
      const rawPost = isEditingContent ? this.inlineText : (req.postResponseScript || '');
      content = renderInlineBlock(rawPost, isEditingContent, this.inlineCursor, '// Post-response JavaScript');
    } else if (this.activeRequestTab === 'docs') {
      const rawDocs = isEditingContent ? this.inlineText : (req.docs || '');
      content = renderInlineBlock(rawDocs, isEditingContent, this.inlineCursor, '# Request Documentation');
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
      `{bold}[Space]{/} Send  {bold}[Click/U]{/} URL  {bold}[Click/I]{/} Edit Tab  ` +
      `{bold}[S]{/} Save  {bold}[R]{/} Revert  {bold}[E]{/} Env  {bold}[C]{/} Coll  {bold}[1-7]{/} Tabs  {bold}[q]{/} Quit`;

    this.statusBar.setContent(
      ` ${shortcuts}   │   {bold}{${t.accent}-fg}${msg}{/}`
    );
    this.screen.render();
  }

  private markRequestModified(req: BrunoRequest) {
    this.modifiedRequests.add(req.id);
    this.renderSidebar();
    this.updateRequestView();
  }

  private getTabEditableText(req: BrunoRequest): string {
    if (this.activeRequestTab === 'headers') {
      return req.headers.map(h => `${h.name}: ${h.value}`).join('\n');
    }
    if (this.activeRequestTab === 'params') {
      return req.params.map(p => `${p.name}=${p.value}`).join('\n');
    }
    if (this.activeRequestTab === 'body') {
      return req.bodyContent || '';
    }
    if (this.activeRequestTab === 'pre-script') {
      return req.preRequestScript || '';
    }
    if (this.activeRequestTab === 'post-script') {
      return req.postResponseScript || '';
    }
    if (this.activeRequestTab === 'docs') {
      return req.docs || '';
    }
    return '';
  }

  private applyTabEditableText(req: BrunoRequest, text: string) {
    if (this.activeRequestTab === 'headers') {
      req.headers = text
        .split(/\r?\n/)
        .filter(l => l.trim().length > 0)
        .map(l => {
          const sep = l.indexOf(':');
          if (sep === -1) return { name: l.trim(), value: '', enabled: true };
          return {
            name: l.slice(0, sep).trim(),
            value: l.slice(sep + 1).trim(),
            enabled: true
          };
        });
    } else if (this.activeRequestTab === 'params') {
      req.params = text
        .split(/\r?\n/)
        .filter(l => l.trim().length > 0)
        .map(l => {
          const sep = l.indexOf('=');
          if (sep === -1) return { name: l.trim(), value: '', enabled: true };
          return {
            name: l.slice(0, sep).trim(),
            value: l.slice(sep + 1).trim(),
            enabled: true
          };
        });
    } else if (this.activeRequestTab === 'body') {
      if (req.bodyType === 'none' && text.trim().length > 0) {
        req.bodyType = text.trim().startsWith('{') ? 'json' : 'text';
      }
      req.bodyContent = text;
    } else if (this.activeRequestTab === 'pre-script') {
      req.preRequestScript = text;
    } else if (this.activeRequestTab === 'post-script') {
      req.postResponseScript = text;
    } else if (this.activeRequestTab === 'docs') {
      req.docs = text;
    }
    this.modifiedRequests.add(req.id);
    this.renderSidebar();
  }

  private applyCollectionHeadersText(text: string) {
    this.collection.collectionHeaders = text
      .split(/\r?\n/)
      .filter(l => l.trim().length > 0)
      .map(l => {
        const sep = l.indexOf(':');
        if (sep === -1) return { name: l.trim(), value: '', enabled: true };
        return {
          name: l.slice(0, sep).trim(),
          value: l.slice(sep + 1).trim(),
          enabled: true
        };
      });
  }

  private activateInlineEdit(target: 'url' | 'content' | 'coll-headers', clickRow?: number, clickCol?: number) {
    const req = this.selectedRequest;
    if (!req) return;
    if (target === 'content' && this.activeRequestTab === 'auth') return;

    const t = this.getTheme();
    this.inlineTarget = target;

    if (target === 'url') {
      this.inlineText = req.url;
      this.inlineCursor = clickCol !== undefined
        ? Math.max(0, Math.min(this.inlineText.length, clickCol))
        : this.inlineText.length;
      this.requestHeaderBox.focus();
    } else if (target === 'coll-headers') {
      this.inlineText = (this.collection.collectionHeaders || [])
        .filter(h => h.enabled !== false)
        .map(h => `${h.name}: ${h.value}`)
        .join('\n');
      if (clickRow !== undefined && clickCol !== undefined) {
        const lines = this.inlineText.split('\n');
        const r = Math.max(0, Math.min(lines.length - 1, clickRow));
        const c = Math.max(0, Math.min(lines[r].length, clickCol));
        let offset = 0;
        for (let i = 0; i < r; i++) {
          offset += lines[i].length + 1;
        }
        this.inlineCursor = offset + c;
      } else {
        this.inlineCursor = this.inlineText.length;
      }
      this.requestContentBox.focus();
    } else {
      if (this.activeRequestTab === 'body' && req.bodyType === 'none') {
        req.bodyType = 'json';
        if (!req.bodyContent) {
          req.bodyContent = '{\n  \n}';
        }
      }
      this.inlineText = this.getTabEditableText(req);
      if (clickRow !== undefined && clickCol !== undefined) {
        const lines = this.inlineText.split('\n');
        const r = Math.max(0, Math.min(lines.length - 1, clickRow));
        const c = Math.max(0, Math.min(lines[r].length, clickCol));
        let offset = 0;
        for (let i = 0; i < r; i++) {
          offset += lines[i].length + 1;
        }
        this.inlineCursor = offset + c;
      } else {
        this.inlineCursor = this.inlineText.length;
      }
      this.requestContentBox.focus();
    }

    this.requestPane.style.border.fg = t.borderFocus;
    this.sidebarList.style.border.fg = t.border;
    this.updateRequestView();
    const targetLabel = target === 'url' ? 'URL' : target === 'coll-headers' ? 'collection.bru Headers' : this.activeRequestTab;
    this.updateStatusBar(`Editing ${targetLabel} inline — Type directly | [Ctrl+S] Save | [Esc] Done`);
  }

  private deactivateInlineEdit() {
    if (this.inlineTarget === 'none') return;
    const t = this.getTheme();
    this.inlineTarget = 'none';
    this.requestPane.style.border.fg = t.border;
    this.sidebarList.style.border.fg = t.borderFocus;
    this.sidebarList.focus();
    this.updateRequestView();
    this.updateStatusBar('Ready');
  }

  private bindEvents() {
    // 1. Sidebar keyboard Enter -> toggle folder or select request
    this.sidebarList.key(['enter'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
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
        if (this.inlineTarget !== 'none') {
          this.inlineText = this.inlineTarget === 'url'
            ? target.request.url
            : this.getTabEditableText(target.request);
          this.inlineCursor = this.inlineText.length;
        }
        this.updateRequestView();
      }
    });

    // 2. Mouse click on any item in sidebar -> single-click opens API or toggles folder
    this.sidebarList.on('element click', (el: any) => {
      if (this.isModalOpen()) return;
      this.deactivateInlineEdit();
      const items = (this.sidebarList as any).items as any[];
      const idx = items ? items.indexOf(el) : -1;
      if (idx >= 0) {
        this.selectedTreeIndex = idx;
        this.handleTreeSelection(idx);
      }
    });

    // 3. Mouse click on Header -> Open Collection or Env based on click position
    this.headerBox.on('click', (data: any) => {
      if (this.isModalOpen()) return;
      this.deactivateInlineEdit();
      const pos = (this.headerBox as any).lpos;
      if (!pos || !data || typeof data.x !== 'number') {
        this.openCollectionModal();
        return;
      }
      const relX = data.x - (pos.xi + 1);
      const relY = data.y - (pos.yi + 1);

      if (relY === 1) {
        // Line 2: " <9-col-dog>  Collection [C]: [name]   Env [E]: [env]"
        const collEnd = 12 + 16 + 1 + this.collection.name.length + 1;
        const envStart = collEnd + 3;
        if (relX >= envStart) {
          this.openEnvironmentModal();
          return;
        }
        this.openCollectionModal();
      }
    });

    // 3b. Mouse click on Request Header Box -> Method, inline URL edit, [Send], [Save .bru], [Revert]
    this.requestHeaderBox.on('click', async (data: any) => {
      if (this.isModalOpen() || !this.selectedRequest) return;
      const pos = (this.requestHeaderBox as any).lpos;
      if (!pos || !data || typeof data.x !== 'number') return;

      const relX = data.x - pos.xi;
      const relY = data.y - pos.yi;

      if (relY === 0) {
        const methodBtnLen = this.selectedRequest.method.length + 5;
        if (relX <= methodBtnLen) {
          this.cycleActiveRequestMethod();
          return;
        }
        const urlCol = Math.max(0, relX - (methodBtnLen + 2));
        this.activateInlineEdit('url', 0, urlCol);
      } else if (relY === 1) {
        // Line 2: " [Send]  [Save .bru (Ctrl+S)]  [Revert]"
        if (relX < 9) {
          await this.sendActiveRequest();
        } else if (relX < 31) {
          this.saveActiveRequest();
        } else if (relX < 41) {
          this.revertActiveRequest();
        } else {
          this.activateInlineEdit('url');
        }
      }
    });

    // 4. Mouse click on Request Tabs -> Select exact clicked tab and enter direct inline editing
    this.requestTabsBox.on('click', (data: any) => {
      if (this.isModalOpen()) return;
      const pos = (this.requestTabsBox as any).lpos;
      if (pos && data && typeof data.x === 'number') {
        const relX = data.x - pos.xi;
        const hit = this.requestTabRanges.find(r => relX >= r.start && relX <= r.end);
        if (hit) {
          this.activeRequestTab = hit.id;
          if (hit.id === 'auth') {
            this.deactivateInlineEdit();
            this.updateRequestView();
          } else {
            this.activateInlineEdit('content');
          }
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

    // 6. Direct Inline Editor Keypress Handler (URL, Headers, Params, Body, Scripts, Docs)
    this.screen.on('keypress', (ch: any, key: any) => {
      if (this.isModalOpen() || this.inlineTarget === 'none' || !this.selectedRequest) return;
      const req = this.selectedRequest;
      const kName = key?.name || '';

      if (kName === 'escape') {
        this.deactivateInlineEdit();
        return;
      }

      if ((key?.ctrl && kName === 's') || kName === 'f2') {
        this.saveActiveRequest();
        return;
      }

      if (key?.ctrl && kName === 'r') {
        this.revertActiveRequest();
        return;
      }

      if (kName === 'return') {
        return;
      }

      const isSingleLine = this.inlineTarget === 'url';

      if (kName === 'enter') {
        if (isSingleLine) {
          this.deactivateInlineEdit();
        } else {
          this.inlineText = this.inlineText.slice(0, this.inlineCursor) + '\n' + this.inlineText.slice(this.inlineCursor);
          this.inlineCursor += 1;
          if (this.inlineTarget === 'coll-headers') {
            this.applyCollectionHeadersText(this.inlineText);
          } else {
            this.applyTabEditableText(req, this.inlineText);
          }
          this.updateRequestView();
        }
        return;
      }

      if (kName === 'backspace') {
        if (this.inlineCursor > 0) {
          this.inlineText = this.inlineText.slice(0, this.inlineCursor - 1) + this.inlineText.slice(this.inlineCursor);
          this.inlineCursor -= 1;
          if (isSingleLine) {
            req.url = this.inlineText;
            this.modifiedRequests.add(req.id);
            this.renderSidebar();
          } else if (this.inlineTarget === 'coll-headers') {
            this.applyCollectionHeadersText(this.inlineText);
          } else {
            this.applyTabEditableText(req, this.inlineText);
          }
          this.updateRequestView();
        }
        return;
      }

      if (kName === 'delete') {
        if (this.inlineCursor < this.inlineText.length) {
          this.inlineText = this.inlineText.slice(0, this.inlineCursor) + this.inlineText.slice(this.inlineCursor + 1);
          if (isSingleLine) {
            req.url = this.inlineText;
            this.modifiedRequests.add(req.id);
            this.renderSidebar();
          } else if (this.inlineTarget === 'coll-headers') {
            this.applyCollectionHeadersText(this.inlineText);
          } else {
            this.applyTabEditableText(req, this.inlineText);
          }
          this.updateRequestView();
        }
        return;
      }

      if (kName === 'left') {
        if (this.inlineCursor > 0) {
          this.inlineCursor -= 1;
          this.updateRequestView();
        }
        return;
      }

      if (kName === 'right') {
        if (this.inlineCursor < this.inlineText.length) {
          this.inlineCursor += 1;
          this.updateRequestView();
        }
        return;
      }

      if (kName === 'home' || (key?.ctrl && kName === 'a')) {
        const lineStart = this.inlineText.lastIndexOf('\n', this.inlineCursor - 1) + 1;
        this.inlineCursor = lineStart;
        this.updateRequestView();
        return;
      }

      if (kName === 'end' || (key?.ctrl && kName === 'e')) {
        const nextNl = this.inlineText.indexOf('\n', this.inlineCursor);
        this.inlineCursor = nextNl === -1 ? this.inlineText.length : nextNl;
        this.updateRequestView();
        return;
      }

      if (!isSingleLine && (kName === 'up' || kName === 'down')) {
        const lines = this.inlineText.split('\n');
        let rem = this.inlineCursor;
        let curRow = 0;
        let curCol = 0;
        for (let i = 0; i < lines.length; i++) {
          if (rem <= lines[i].length) {
            curRow = i;
            curCol = rem;
            break;
          }
          rem -= lines[i].length + 1;
        }
        const targetRow = kName === 'up' ? Math.max(0, curRow - 1) : Math.min(lines.length - 1, curRow + 1);
        const targetCol = Math.min(curCol, lines[targetRow].length);
        let newCursor = 0;
        for (let i = 0; i < targetRow; i++) {
          newCursor += lines[i].length + 1;
        }
        this.inlineCursor = newCursor + targetCol;
        this.updateRequestView();
        return;
      }

      if (ch && !key?.ctrl && !key?.meta && !/^[\x00-\x1f\x7f]$/.test(ch)) {
        this.inlineText = this.inlineText.slice(0, this.inlineCursor) + ch + this.inlineText.slice(this.inlineCursor);
        this.inlineCursor += ch.length;
        if (isSingleLine) {
          req.url = this.inlineText;
          this.modifiedRequests.add(req.id);
          this.renderSidebar();
        } else if (this.inlineTarget === 'coll-headers') {
          this.applyCollectionHeadersText(this.inlineText);
        } else {
          this.applyTabEditableText(req, this.inlineText);
        }
        this.updateRequestView();
      }
    });

    // 7. Global Hotkeys (active when not in modal and not typing inline)
    this.screen.key(['C-c'], () => {
      return process.exit(0);
    });

    this.screen.key(['q'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      return process.exit(0);
    });

    // Space key: Send request
    this.screen.key(['space'], async () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      await this.sendActiveRequest();
    });

    // Open Collection Picker
    this.screen.key(['c', 'C'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      this.openCollectionModal();
    });

    // OAuth2 Trigger
    this.screen.key(['o', 'O'], async () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      await this.triggerOAuth2();
    });

    // Cycle Theme
    this.screen.key(['t', 'T'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      const newTheme = this.themeManager.cycle();
      this.applyTheme();
      this.renderSidebar();
      this.updateRequestView();
      this.updateResponseView();
      this.updateStatusBar(`Theme: ${newTheme.name}`);
    });

    // Environment Modal
    this.screen.key(['e', 'E'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      this.openEnvironmentModal();
    });

    // Edit URL [U]
    this.screen.key(['u', 'U'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      this.activateInlineEdit('url');
    });

    // Edit active tab content [I]
    this.screen.key(['i', 'I'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      this.activateInlineEdit('content');
    });

    // Number keys for request tabs
    this.screen.key(['1'], () => { if (!this.isModalOpen() && this.inlineTarget === 'none') { this.activeRequestTab = 'headers'; this.updateRequestView(); } });
    this.screen.key(['2'], () => { if (!this.isModalOpen() && this.inlineTarget === 'none') { this.activeRequestTab = 'params'; this.updateRequestView(); } });
    this.screen.key(['3'], () => { if (!this.isModalOpen() && this.inlineTarget === 'none') { this.activeRequestTab = 'body'; this.updateRequestView(); } });
    this.screen.key(['4'], () => { if (!this.isModalOpen() && this.inlineTarget === 'none') { this.activeRequestTab = 'auth'; this.updateRequestView(); } });
    this.screen.key(['5'], () => { if (!this.isModalOpen() && this.inlineTarget === 'none') { this.activeRequestTab = 'pre-script'; this.updateRequestView(); } });
    this.screen.key(['6'], () => { if (!this.isModalOpen() && this.inlineTarget === 'none') { this.activeRequestTab = 'post-script'; this.updateRequestView(); } });
    this.screen.key(['7'], () => { if (!this.isModalOpen() && this.inlineTarget === 'none') { this.activeRequestTab = 'docs'; this.updateRequestView(); } });

    // Tab key toggles between Sidebar and Direct Inline Editor
    this.screen.key(['tab'], () => {
      if (this.isModalOpen()) return;
      if (this.inlineTarget !== 'none') {
        this.deactivateInlineEdit();
      } else {
        this.activateInlineEdit('content');
      }
    });

    // Mouse click on Request Content Box -> Direct inline editing at exact clicked line & column!
    this.requestContentBox.on('click', async (data: any) => {
      if (this.isModalOpen() || !this.selectedRequest) return;
      const req = this.selectedRequest;

      const pos = (this.requestContentBox as any).lpos;
      if (!pos || !data || typeof data.x !== 'number' || typeof data.y !== 'number') return;

      const relX = data.x - pos.xi;
      const relY = data.y - pos.yi + ((this.requestContentBox as any).childBase || 0);
      const lines = (this.requestContentBox.getContent() || '').split('\n');
      const clickedLine = lines[relY] || '';

      if (this.activeRequestTab === 'headers') {
        const rawReqLines = (req.headers || []).map(h => `${h.name}: ${h.value}`);
        const reqLinesCount = Math.max(1, rawReqLines.length);
        const collHeaderSepRow = reqLinesCount + 1; // 1 blank line + 1 separator header
        if (relY > collHeaderSepRow) {
          const collRow = relY - (collHeaderSepRow + 1);
          this.activateInlineEdit('coll-headers', collRow, Math.max(0, relX - 6));
          return;
        } else if (relY === collHeaderSepRow) {
          this.activateInlineEdit('coll-headers', 0, 0);
          return;
        }
        this.activateInlineEdit('content', relY, Math.max(0, relX - 6));
        return;
      }

      if (this.activeRequestTab === 'params' ||
          this.activeRequestTab === 'pre-script' || this.activeRequestTab === 'post-script' ||
          this.activeRequestTab === 'docs') {
        this.activateInlineEdit('content', relY, Math.max(0, relX - 6));
        return;
      }

      if (this.activeRequestTab === 'body') {
        if (relY === 0) {
          const hitBt = this.bodyTypeRanges.find(r => relX >= r.start && relX <= r.end);
          if (hitBt) {
            req.bodyType = hitBt.id;
            this.markRequestModified(req);
            this.activateInlineEdit('content');
            this.updateStatusBar(`Body type set to: ${hitBt.id}`);
            return;
          }
          this.activateInlineEdit('content', 0, 0);
          return;
        }
        this.activateInlineEdit('content', relY - 1, Math.max(0, relX - 6));
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
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      this.openAuthManagerModal();
    });

    // Hotkey [M]: Cycle Auth Mode (if on Auth Tab) or HTTP Method
    this.screen.key(['m', 'M'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none' || !this.selectedRequest) return;
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
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
      this.saveActiveRequest();
    });

    // Hotkey [R]: Revert request from .bru file on disk
    this.screen.key(['r', 'R'], () => {
      if (this.isModalOpen() || this.inlineTarget !== 'none') return;
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
    this.activateInlineEdit('url');
  }

  private editActiveTab() {
    if (this.activeRequestTab === 'auth') {
      this.openAuthManagerModal();
    } else {
      this.activateInlineEdit('content');
    }
  }

  private openSingleLinePrompt(title: string, initialValue: string, onSubmit: (val: string) => void) {
    this.openTextEditorModal(title, initialValue, false, onSubmit);
  }

  private openMultiLineEditor(title: string, initialValue: string, onSubmit: (val: string) => void) {
    this.openTextEditorModal(title, initialValue, true, onSubmit);
  }

  private openTextEditorModal(
    title: string,
    initialValue: string,
    multiline: boolean,
    onSubmit: (val: string) => void
  ) {
    if (this.isModalOpen()) return;
    const t = this.getTheme();

    this.editorModal = blessed.box({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: multiline ? '82%' : '72%',
      height: multiline ? '76%' : 9,
      border: { type: 'line' },
      style: {
        bg: t.surface,
        fg: t.fg,
        border: { fg: t.accent }
      },
      tags: true,
      mouse: true,
      autoFocus: false
    } as any);

    blessed.box({
      parent: this.editorModal,
      top: 0,
      left: 1,
      right: 26,
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
      mouse: true,
      autoFocus: false
    } as any);

    const cancelBtn = blessed.box({
      parent: this.editorModal,
      top: 0,
      right: 1,
      width: 11,
      height: 1,
      content: `{bold}{${t.delete}-fg}[X Cancel]{/}`,
      style: { bg: t.surface, fg: t.delete },
      tags: true,
      mouse: true,
      autoFocus: false
    } as any);

    const editorBox = blessed.box({
      parent: this.editorModal,
      top: 1,
      left: 1,
      right: 1,
      bottom: 2,
      border: { type: 'line' },
      tags: true,
      mouse: true,
      scrollable: true,
      alwaysScroll: true,
      style: {
        bg: t.bg,
        fg: t.fg,
        border: { fg: t.borderFocus }
      }
    });

    const previewBox = blessed.box({
      parent: this.editorModal,
      bottom: 1,
      left: 1,
      right: 1,
      height: 1,
      tags: true,
      style: { bg: t.surface, fg: t.subtext }
    });

    const helpText = multiline
      ? `{${t.subtext}-fg}[Type] Edit   [Arrows] Move   [Enter] New Line   {bold}{${t.get}-fg}[Ctrl+S / Click Apply]{/} Confirm   {bold}{${t.delete}-fg}[Esc]{/} Cancel{/}`
      : `{${t.subtext}-fg}[Type] Edit   [Left/Right] Move   {bold}{${t.get}-fg}[Enter / Ctrl+S / Click Apply]{/} Confirm   {bold}{${t.delete}-fg}[Esc]{/} Cancel{/}`;

    blessed.box({
      parent: this.editorModal,
      bottom: 0,
      left: 1,
      right: 1,
      height: 1,
      content: helpText,
      style: { bg: t.surface, fg: t.subtext },
      tags: true
    });

    let text = initialValue || '';
    let cursor = text.length;
    let editorKeyHandler: ((ch: any, key: any) => void) | undefined;

    const escapeTags = (s: string) => s.replace(/\{/g, '{open}').replace(/\}/g, '{close}');

    const renderLineWithCursor = (line: string, col: number | null): string => {
      if (col === null) {
        const escaped = line.replace(/\{/g, '\x01').replace(/\}/g, '\x02');
        const highlighted = escaped.replace(/\x01\x01([a-zA-Z0-9_$.,-]+)\x02\x02/g, (_m, varName) => {
          const isDyn = varName.startsWith('$');
          const resolved = isDyn ? '<dynamic>' : Interpolator.getVariable(varName, this.collection);
          const c = resolved !== undefined ? t.accent : t.delete;
          return `{bold}{${c}-fg}{open}{open}${varName}{close}{close}{/${c}-fg}{/bold}`;
        });
        return highlighted.replace(/\x01/g, '{open}').replace(/\x02/g, '{close}');
      }

      const before = escapeTags(line.slice(0, col));
      const atChar = col < line.length ? escapeTags(line[col]) : ' ';
      const after = col < line.length ? escapeTags(line.slice(col + 1)) : '';
      return `${before}{inverse}${atChar}{/inverse}${after}`;
    };

    const renderEditor = () => {
      const lines = text.split('\n');
      let rem = cursor;
      let curRow = 0;
      let curCol = 0;
      for (let i = 0; i < lines.length; i++) {
        if (rem <= lines[i].length) {
          curRow = i;
          curCol = rem;
          break;
        }
        rem -= lines[i].length + 1;
      }

      const renderedLines = lines.map((line, idx) => {
        const linePrefix = multiline
          ? `{${t.subtext}-fg}${String(idx + 1).padStart(2, ' ')} │ {/}`
          : ' ';
        const body = renderLineWithCursor(line, idx === curRow ? curCol : null);
        return `${linePrefix}${body}`;
      });

      editorBox.setContent(renderedLines.join('\n'));

      if (multiline) {
        const pos = (editorBox as any).lpos;
        const visH = pos ? Math.max(1, (pos.yl - pos.yi) - 2) : 10;
        if (curRow >= visH) {
          editorBox.setScroll(curRow - visH + 2);
        } else {
          editorBox.setScroll(0);
        }
      }

      // Live variable preview bar
      const varMatches = Array.from(text.matchAll(/\{\{([a-zA-Z0-9_$.,-]+)\}\}/g)).map(m => m[1]);
      const uniqueVars = Array.from(new Set(varMatches));
      if (uniqueVars.length > 0) {
        const previews = uniqueVars.map(v => {
          const val = v.startsWith('$') ? '<dynamic>' : Interpolator.getVariable(v, this.collection);
          if (val !== undefined) {
            return `{bold}{${t.accent}-fg}{open}{open}${v}{close}{close}{/} = {bold}{${t.accent}-fg}${escapeTags(val.slice(0, 35))}{/}`;
          }
          return `{bold}{${t.delete}-fg}{open}{open}${v}{close}{close} (unresolved){/}`;
        }).join('   ');
        previewBox.setContent(` {${t.subtext}-fg}Vars:{/} ${previews}`);
      } else {
        previewBox.setContent(` {${t.subtext}-fg}Tip: Use {bold}{${t.accent}-fg}{open}{open}VAR_NAME{close}{close}{/}{${t.subtext}-fg} to reference active environment variables.{/}`);
      }

      this.screen.render();
    };

    const closeEditor = () => {
      if (editorKeyHandler) {
        this.screen.removeListener('keypress', editorKeyHandler);
        editorKeyHandler = undefined;
      }
      if (this.editorModal) {
        this.editorModal.destroy();
        this.editorModal = undefined;
        this.sidebarList.focus();
        this.screen.render();
      }
    };

    const finishSave = () => {
      const finalVal = text;
      closeEditor();
      onSubmit(finalVal);
    };

    applyBtn.on('click', () => {
      finishSave();
    });

    cancelBtn.on('click', () => {
      closeEditor();
    });

    this.editorModal.setFront();
    this.editorModal.focus();
    renderEditor();

    // Defer attaching keypress listener by 1 tick so the opening key ('i' or 'u') is not inserted into the text
    setImmediate(() => {
      if (!this.editorModal) return;
      editorKeyHandler = (ch: any, key: any) => {
        if (!this.editorModal) return;
        const kName = key?.name || '';

        if (kName === 'escape') {
          closeEditor();
          return;
        }

        if ((key?.ctrl && kName === 's') || kName === 'f2') {
          finishSave();
          return;
        }

        if (kName === 'return') {
          return;
        }

        if (kName === 'enter') {
          if (!multiline) {
            finishSave();
          } else {
            text = text.slice(0, cursor) + '\n' + text.slice(cursor);
            cursor += 1;
            renderEditor();
          }
          return;
        }

        if (kName === 'backspace') {
          if (cursor > 0) {
            text = text.slice(0, cursor - 1) + text.slice(cursor);
            cursor -= 1;
            renderEditor();
          }
          return;
        }

        if (kName === 'delete') {
          if (cursor < text.length) {
            text = text.slice(0, cursor) + text.slice(cursor + 1);
            renderEditor();
          }
          return;
        }

        if (kName === 'left') {
          if (cursor > 0) {
            cursor -= 1;
            renderEditor();
          }
          return;
        }

        if (kName === 'right') {
          if (cursor < text.length) {
            cursor += 1;
            renderEditor();
          }
          return;
        }

        if (kName === 'home' || (key?.ctrl && kName === 'a')) {
          const lineStart = text.lastIndexOf('\n', cursor - 1) + 1;
          cursor = lineStart;
          renderEditor();
          return;
        }

        if (kName === 'end' || (key?.ctrl && kName === 'e')) {
          const nextNl = text.indexOf('\n', cursor);
          cursor = nextNl === -1 ? text.length : nextNl;
          renderEditor();
          return;
        }

        if (multiline && (kName === 'up' || kName === 'down')) {
          const lines = text.split('\n');
          let rem = cursor;
          let curRow = 0;
          let curCol = 0;
          for (let i = 0; i < lines.length; i++) {
            if (rem <= lines[i].length) {
              curRow = i;
              curCol = rem;
              break;
            }
            rem -= lines[i].length + 1;
          }
          const targetRow = kName === 'up' ? Math.max(0, curRow - 1) : Math.min(lines.length - 1, curRow + 1);
          const targetCol = Math.min(curCol, lines[targetRow].length);
          let newCursor = 0;
          for (let i = 0; i < targetRow; i++) {
            newCursor += lines[i].length + 1;
          }
          newCursor += targetCol;
          cursor = newCursor;
          renderEditor();
          return;
        }

        if (ch && !key?.ctrl && !key?.meta && !/^[\x00-\x1f\x7f]$/.test(ch)) {
          text = text.slice(0, cursor) + ch + text.slice(cursor);
          cursor += ch.length;
          renderEditor();
        }
      };

      this.screen.on('keypress', editorKeyHandler);
    });
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
    if (this.inlineTarget === 'coll-headers') {
      this.saveCollectionHeaders();
      return;
    }
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
    if (this.inlineTarget === 'coll-headers') {
      this.revertCollectionHeaders();
      return;
    }
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
      if (this.inlineTarget === 'url') {
        this.inlineText = req.url;
        this.inlineCursor = this.inlineText.length;
      } else if (this.inlineTarget === 'content') {
        this.inlineText = this.getTabEditableText(req);
        this.inlineCursor = this.inlineText.length;
      }
      this.renderSidebar();
      this.updateRequestView();
      this.updateStatusBar(`Reverted ${req.name} from ${path.basename(req.filePath)}`);
    } catch (err: any) {
      this.updateStatusBar(`Revert error: ${err.message}`);
    }
  }

  private saveCollectionHeaders() {
    const collBruPath = path.join(this.collectionPath, 'collection.bru');
    try {
      let json: any = {};
      if (fs.existsSync(collBruPath)) {
        const raw = fs.readFileSync(collBruPath, 'utf8');
        json = collectionBruToJson(raw) || {};
      }
      if (!json.meta) {
        json.meta = { name: this.collection.name, type: 'collection' };
      }
      json.headers = (this.collection.collectionHeaders || []).map(h => ({
        name: h.name,
        value: h.value,
        enabled: h.enabled !== false
      }));

      const updated = jsonToCollectionBru(json);
      fs.writeFileSync(collBruPath, updated, 'utf8');
      this.updateRequestView();
      this.updateStatusBar(`Saved collection headers to collection.bru`);
    } catch (err: any) {
      this.updateStatusBar(`Save error (collection.bru): ${err.message}`);
    }
  }

  private revertCollectionHeaders() {
    const collBruPath = path.join(this.collectionPath, 'collection.bru');
    try {
      if (fs.existsSync(collBruPath)) {
        const raw = fs.readFileSync(collBruPath, 'utf8');
        const parsed = collectionBruToJson(raw) || {};
        if (parsed.headers && Array.isArray(parsed.headers)) {
          this.collection.collectionHeaders = parsed.headers.map((h: any) => ({
            name: h.name || '',
            value: h.value || '',
            enabled: h.enabled !== false
          }));
        } else {
          this.collection.collectionHeaders = [];
        }
      }
      if (this.inlineTarget === 'coll-headers') {
        this.inlineText = (this.collection.collectionHeaders || [])
          .filter(h => h.enabled !== false)
          .map(h => `${h.name}: ${h.value}`)
          .join('\n');
        this.inlineCursor = this.inlineText.length;
      }
      this.updateRequestView();
      this.updateStatusBar(`Reverted collection headers from collection.bru`);
    } catch (err: any) {
      this.updateStatusBar(`Revert error (collection.bru): ${err.message}`);
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
