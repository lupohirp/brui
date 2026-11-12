import blessed from 'neo-blessed';
import * as path from 'node:path';
import { BrunoCollection, BrunoFolder, BrunoRequest, HttpResponse, ConsoleLogEntry, ThemeColors } from '../types';
import { ThemeManager } from '../themes';
import { CollectionParser, DiscoveredCollection } from '../core/parser';
import { EnvironmentManagerModal } from './env-manager';
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

  // UI Widgets
  private screen!: blessed.Widgets.Screen;
  private headerBox!: blessed.Widgets.BoxElement;
  private sidebarList!: blessed.Widgets.ListElement;
  private requestPane!: blessed.Widgets.BoxElement;
  private requestTabsBox!: blessed.Widgets.BoxElement;
  private requestContentBox!: blessed.Widgets.BoxElement;
  private responsePane!: blessed.Widgets.BoxElement;
  private responseHeaderBox!: blessed.Widgets.BoxElement;
  private responseTabsBox!: blessed.Widgets.BoxElement;
  private responseContentBox!: blessed.Widgets.BoxElement;
  private statusBar!: blessed.Widgets.BoxElement;

  private collectionModal?: blessed.Widgets.BoxElement;
  private envManagerModal?: EnvironmentManagerModal;
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

    // Expand top-level folders by default
    this.expandedFolders.clear();
    for (const f of this.collection.folders) {
      this.expandedFolders.add(f.id);
    }

    this.rebuildTree();

    // Select first available request
    const firstReq = this.findFirstRequest(this.collection);
    this.selectedRequest = firstReq;

    if (this.screen) {
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

    // 1. Top Header (Height 3)
    this.headerBox = blessed.box({
      parent: this.screen,
      top: 0,
      left: 0,
      width: '100%',
      height: 3,
      tags: true,
      border: { type: 'line' },
      mouse: true
    });

    // 2. Left Sidebar (Tree view with folders & requests)
    this.sidebarList = blessed.list({
      parent: this.screen,
      top: 3,
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
        track: { bg: theme.surface },
        style: { inverse: true }
      }
    });

    // 3. Request Pane (Top Right)
    this.requestPane = blessed.box({
      parent: this.screen,
      top: 3,
      left: '32%',
      width: '68%',
      height: '48%',
      border: { type: 'line' },
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
        track: { bg: theme.surface },
        style: { inverse: true }
      }
    });

    // 4. Response Pane (Bottom Right)
    this.responsePane = blessed.box({
      parent: this.screen,
      top: '51%',
      left: '32%',
      width: '68%',
      bottom: 2,
      border: { type: 'line' },
      tags: true,
      mouse: true
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
        track: { bg: theme.surface },
        style: { inverse: true }
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

    this.screen.program.bg(t.bg);
    this.screen.program.fg(t.fg);

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
      hover: { bg: t.surfaceHover }
    };

    this.requestPane.style.bg = t.bg;
    this.requestPane.style.fg = t.fg;
    this.requestPane.style.border.fg = t.border;
    this.requestContentBox.style.bg = t.surface;
    this.requestContentBox.style.fg = t.fg;

    this.responsePane.style.bg = t.bg;
    this.responsePane.style.fg = t.fg;
    this.responsePane.style.border.fg = t.border;
    this.responseContentBox.style.bg = t.surface;
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
      ? `{${t.get}-fg}● Authenticated{/}`
      : `{${t.subtext}-fg}○ Not Authenticated{/}`;

    this.headerBox.setContent(
      ` {bold}{${t.accent}-fg}⚡ BRUI{/}  ` +
      `{${t.subtext}-fg}Collection:{/} {bold}{${t.put}-fg}[📁 ${this.collection.name}]{/}  ` +
      `{${t.subtext}-fg}Env:{/} {bold}{${t.options}-fg}[${envName}]{/}  ` +
      `{${t.subtext}-fg}Auth:{/} ${authStatus}  ` +
      `{right}{${t.subtext}-fg}Theme:{/} {${t.put}-fg}${t.name}{/} `
    );
  }

  private renderSidebar() {
    const t = this.getTheme();
    const items: string[] = [];

    for (const item of this.visibleTreeItems) {
      const indent = '  '.repeat(item.depth);
      if (item.type === 'folder') {
        const isExpanded = this.expandedFolders.has(item.folder.id);
        const icon = isExpanded ? '📂' : '📁';
        const arrow = isExpanded ? '▼' : '▶';
        const total = item.folder.requests.length + item.folder.folders.length;
        items.push(`${indent}{bold}{${t.accent}-fg}${arrow} ${icon} ${item.folder.name}{/} {${t.subtext}-fg}(${total}){/}`);
      } else {
        const req = item.request;
        let badgeColor = t.get;
        if (req.method === 'POST') badgeColor = t.post;
        else if (req.method === 'PUT') badgeColor = t.put;
        else if (req.method === 'PATCH') badgeColor = t.patch;
        else if (req.method === 'DELETE') badgeColor = t.delete;

        items.push(`${indent} {bold}{${badgeColor}-fg}${req.method.padEnd(5)}{/} ${req.name}`);
      }
    }

    this.sidebarList.setItems(items);
    this.screen.render();
  }

  private updateRequestView() {
    const t = this.getTheme();
    const req = this.selectedRequest;
    if (!req) {
      this.requestPane.setContent(' No request selected. Expand a folder or select a request.');
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

    const interpolatedUrl = Interpolator.interpolate(req.url, this.collection);

    this.requestPane.setContent(
      ` {bold}{${badgeColor}-fg}${req.method}{/}  {bold}${interpolatedUrl}{/}`
    );

    const tabs = [
      { id: 'headers', label: `Headers (${req.headers.length})` },
      { id: 'params', label: `Params (${req.params.length})` },
      { id: 'body', label: `Body (${req.bodyType})` },
      { id: 'auth', label: `Auth (${req.authType})` },
      { id: 'pre-script', label: `Pre-Script${req.preRequestScript ? ' ●' : ''}` },
      { id: 'post-script', label: `Post-Script${req.postResponseScript ? ' ●' : ''}` },
      { id: 'docs', label: `Docs` }
    ];

    const tabStr = tabs.map(tab => {
      if (tab.id === this.activeRequestTab) {
        return `{bold}{${t.accent}-fg}[ ${tab.label} ]{/}`;
      }
      return `{${t.subtext}-fg}  ${tab.label}  {/}`;
    }).join(' ');

    this.requestTabsBox.setContent(tabStr);

    let content = '';
    if (this.activeRequestTab === 'headers') {
      if (req.headers.length === 0) content = '{#6c7086-fg}No custom headers.{/}';
      else {
        content = req.headers.map(h => {
          const val = Interpolator.interpolate(h.value, this.collection);
          return `{bold}{#89b4fa-fg}${h.name}{/}: {#cdd6f4-fg}${val}{/}`;
        }).join('\n');
      }
    } else if (this.activeRequestTab === 'params') {
      if (req.params.length === 0) content = '{#6c7086-fg}No query parameters.{/}';
      else {
        content = req.params.map(p => {
          const val = Interpolator.interpolate(p.value, this.collection);
          return `{bold}{#94e2d5-fg}${p.name}{/} = {#cdd6f4-fg}${val}{/}`;
        }).join('\n');
      }
    } else if (this.activeRequestTab === 'body') {
      if (req.bodyType === 'none' || !req.bodyContent) {
        content = '{#6c7086-fg}No body configured.{/}';
      } else {
        content = Interpolator.interpolate(req.bodyContent, this.collection);
      }
    } else if (this.activeRequestTab === 'auth') {
      content = `{bold}Auth Type:{/} ${req.authType}\n`;
      if (req.authType === 'bearer' || req.authType === 'inherit') {
        const token = req.authBearerToken || this.collection.collectionAuthBearerToken;
        const resolved = token ? Interpolator.interpolate(token, this.collection) : '(none)';
        content += `{bold}Bearer Token:{/} ${resolved}\n`;
      } else if (req.authType === 'oauth2' && req.authOAuth2) {
        content += `{bold}Grant Type:{/} ${req.authOAuth2.grantType}\n`;
        content += `{bold}Token URL:{/} ${req.authOAuth2.tokenUrl}\n`;
        content += `{bold}Client ID:{/} ${req.authOAuth2.clientId}\n`;
      }
    } else if (this.activeRequestTab === 'pre-script') {
      content = req.preRequestScript || '{#6c7086-fg}No pre-request script.{/}';
    } else if (this.activeRequestTab === 'post-script') {
      content = req.postResponseScript || '{#6c7086-fg}No post-response script.{/}';
    } else if (this.activeRequestTab === 'docs') {
      content = req.docs || '{#6c7086-fg}No documentation available.{/}';
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

    const tabs = [
      { id: 'body', label: 'Response Body' },
      { id: 'headers', label: `Headers (${Object.keys(resp.headers).length})` },
      { id: 'console', label: `Console (${this.scriptRunner.logs.length})` }
    ];

    const tabStr = tabs.map(tab => {
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
    this.statusBar.setContent(
      ` {bold}[Space]{/} Send  ` +
      `{bold}[C]{/} Open Coll  ` +
      `{bold}[O]{/} OAuth2  ` +
      `{bold}[E]{/} Env  ` +
      `{bold}[T]{/} Theme  ` +
      `{bold}[1-7]{/} Tabs  ` +
      `{bold}[q]{/} Quit   ` +
      `{right}{bold}{${t.accent}-fg}${msg}{/} `
    );
    this.screen.render();
  }

  private bindEvents() {
    // 1. Sidebar selection change & Enter handling
    this.sidebarList.on('select', (_item: any, index: number) => {
      this.handleTreeSelection(index);
    });

    this.sidebarList.on('select item', (_item: any, index: number) => {
      this.selectedTreeIndex = index;
      const target = this.visibleTreeItems[index];
      if (target && target.type === 'request') {
        this.selectedRequest = target.request;
        this.updateRequestView();
      }
    });

    // 2. Mouse click on sidebar
    this.sidebarList.on('click', () => {
      const idx = (this.sidebarList as any).selected;
      this.handleTreeSelection(idx);
    });

    // 3. Mouse click on Header -> Opens Collection Picker!
    this.headerBox.on('click', () => {
      this.openCollectionModal();
    });

    // 4. Mouse click on Request Tabs
    this.requestTabsBox.on('click', () => {
      const tabs: Array<typeof this.activeRequestTab> = ['headers', 'params', 'body', 'auth', 'pre-script', 'post-script', 'docs'];
      const curIdx = tabs.indexOf(this.activeRequestTab);
      this.activeRequestTab = tabs[(curIdx + 1) % tabs.length];
      this.updateRequestView();
    });

    // 5. Mouse click on Response Tabs
    this.responseTabsBox.on('click', () => {
      const tabs: Array<typeof this.activeResponseTab> = ['body', 'headers', 'console'];
      const curIdx = tabs.indexOf(this.activeResponseTab);
      this.activeResponseTab = tabs[(curIdx + 1) % tabs.length];
      this.updateResponseView();
    });

    // 6. Global Hotkeys
    this.screen.key(['q', 'C-c'], () => {
      return process.exit(0);
    });

    // Space key: Send request
    this.screen.key(['space'], async () => {
      await this.sendActiveRequest();
    });

    // Open Collection Picker
    this.screen.key(['c', 'C'], () => {
      this.openCollectionModal();
    });

    // OAuth2 Trigger
    this.screen.key(['o', 'O'], async () => {
      await this.triggerOAuth2();
    });

    // Cycle Theme
    this.screen.key(['t', 'T'], () => {
      const newTheme = this.themeManager.cycle();
      this.applyTheme();
      this.renderSidebar();
      this.updateRequestView();
      this.updateResponseView();
      this.updateStatusBar(`Theme: ${newTheme.name}`);
    });

    // Environment Modal
    this.screen.key(['e', 'E'], () => {
      this.openEnvironmentModal();
    });

    // Number keys for request tabs
    this.screen.key(['1'], () => { this.activeRequestTab = 'headers'; this.updateRequestView(); });
    this.screen.key(['2'], () => { this.activeRequestTab = 'params'; this.updateRequestView(); });
    this.screen.key(['3'], () => { this.activeRequestTab = 'body'; this.updateRequestView(); });
    this.screen.key(['4'], () => { this.activeRequestTab = 'auth'; this.updateRequestView(); });
    this.screen.key(['5'], () => { this.activeRequestTab = 'pre-script'; this.updateRequestView(); });
    this.screen.key(['6'], () => { this.activeRequestTab = 'post-script'; this.updateRequestView(); });
    this.screen.key(['7'], () => { this.activeRequestTab = 'docs'; this.updateRequestView(); });

    // Tab key switches focus
    this.screen.key(['tab'], () => {
      if (this.screen.focused === this.sidebarList) {
        this.responseContentBox.focus();
      } else if (this.screen.focused === this.responseContentBox) {
        this.requestContentBox.focus();
      } else {
        this.sidebarList.focus();
      }
      this.screen.render();
    });
  }

  private handleTreeSelection(index: number) {
    const item = this.visibleTreeItems[index];
    if (!item) return;

    if (item.type === 'folder') {
      // Toggle folder expansion
      if (this.expandedFolders.has(item.folder.id)) {
        this.expandedFolders.delete(item.folder.id);
      } else {
        this.expandedFolders.add(item.folder.id);
      }
      this.rebuildTree();
      this.renderSidebar();
      (this.sidebarList as any).select(index);
    } else {
      // Selected request
      this.selectedRequest = item.request;
      this.updateRequestView();
    }
  }

  private openCollectionModal() {
    if (this.collectionModal) return;

    const t = this.getTheme();
    const discovered = CollectionParser.discoverCollections(this.collectionPath);

    this.collectionModal = blessed.box({
      parent: this.screen,
      top: 'center',
      left: 'center',
      width: '60%',
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

    const titleBox = blessed.box({
      parent: this.collectionModal,
      top: 0,
      left: 1,
      right: 1,
      height: 2,
      content: `{bold}{${t.accent}-fg}📂 Select a Bruno Collection{/} {${t.subtext}-fg}(click or press Enter, Esc to cancel){/}`,
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
        item: { hover: { bg: t.surfaceHover } }
      },
      items: discovered.map(c => {
        const isCurrent = c.path === this.collectionPath;
        const star = isCurrent ? `{${t.get}-fg}● (current){/} ` : '  ';
        return `${star}{bold}${c.name}{/} {${t.subtext}-fg}${c.path}{/}`;
      })
    });

    const closeModal = () => {
      if (this.collectionModal) {
        this.collectionModal.destroy();
        this.collectionModal = undefined;
        this.sidebarList.focus();
        this.screen.render();
      }
    };

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

    this.screen.onceKey('escape', () => {
      closeModal();
    });

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

  private async triggerOAuth2() {
    const req = this.selectedRequest;
    let oauthConfig = req?.authOAuth2;

    if (!oauthConfig && this.collection.activeEnvironment) {
      const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
      if (env) {
        const tokenUrl = env.variables['APIGEE_HOST'] ? `${env.variables['APIGEE_HOST']}/auth/oauth/v2/token` : undefined;
        const clientId = env.variables['KC_CLIENT_ID'];
        const clientSecret = env.variables['KC_CLIENT_SECRET'];
        if (tokenUrl && clientId) {
          oauthConfig = {
            grantType: 'authorization_code',
            tokenUrl,
            clientId,
            clientSecret,
            authUrl: env.variables['APIGEE_HOST'] ? `${env.variables['APIGEE_HOST']}/auth/oauth/v2/authorize` : undefined
          };
        }
      }
    }

    if (!oauthConfig) {
      this.updateStatusBar('No OAuth2 config found for request or environment');
      return;
    }

    this.updateStatusBar('Starting OAuth2 browser authentication...');
    try {
      const result = await OAuth2Handler.authenticate(oauthConfig, this.collection, (msg) => {
        this.updateStatusBar(msg);
      });
      this.updateHeader();
      this.updateRequestView();
      this.updateStatusBar(`OAuth2 success! Token saved: ${result.accessToken.slice(0, 15)}...`);
    } catch (err: any) {
      this.updateStatusBar(`OAuth2 error: ${err.message}`);
    }
  }
}
